package domain

import (
	"context"
	"maps"
	"slices"
	"time"
)

func (s *AgentWork) Resume(ctx context.Context, admission StartAdmission, port ExecutionPort) error {
	if err := s.AdmitResume(); err != nil {
		return err
	}
	return s.begin(ctx, admission, ContextContinue, port)
}

func (s *AgentWork) Restart(ctx context.Context, admission StartAdmission, port ExecutionPort) error {
	if err := s.AdmitRestart(); err != nil {
		return err
	}
	return s.begin(ctx, admission, ContextRecover, port)
}

// AdmitResume reports whether a Resume may begin now, so its Computer is
// asked for room only for a run that can start.
func (s *AgentWork) AdmitResume() error {
	if s.State != WorkStopped || s.CloseRequestReference != "" {
		return ErrWorkTransition
	}
	return s.admitNewRun()
}

// AdmitRestart reports whether a Restart may begin now.
func (s *AgentWork) AdmitRestart() error {
	if s.State != WorkFailed || s.CloseRequestReference != "" {
		return ErrWorkTransition
	}
	return s.admitNewRun()
}

func (s *AgentWork) admitNewRun() error {
	if s.HarnessProcess != nil && !s.HarnessProcess.ComputeReclaimed {
		return ErrComputeReclamationPending
	}
	return nil
}

func (s *AgentWork) begin(ctx context.Context, admission StartAdmission, mode ContextMode, port ExecutionPort) error {
	if err := validateStartAdmission(s.AgentID, s.WorkModel, admission, port); err != nil {
		return err
	}
	// A Work stays on its Computer: every run is admitted there.
	if admission.Seat.ComputerID != s.ComputerID {
		return ErrExecutionNotPermitted
	}
	if err := s.admitNewRun(); err != nil {
		return err
	}
	// Each start takes up the current Plugin releases it was supplied.
	capabilities, err := s.CapabilitySnapshot.UpgradePlugins(admission.CurrentPlugins)
	if err != nil {
		return err
	}
	s.CapabilitySnapshot = capabilities
	// A question the stopped process left unanswered stays Waiting until a
	// new Turn starts or the user answers it.
	var waitingSince time.Time
	if s.HarnessProcess != nil {
		waitingSince = s.HarnessProcess.WaitingSince
	}
	s.HarnessProcess = admission.process(mode, waitingSince)
	s.StopRequestReference, s.EndRequestReference, s.FailureReason = "", "", ""
	s.StopReason = ""
	s.ReachResumeDue = false
	s.State = WorkStarting
	// A start waiting for its Desk is started once it is seated.
	if s.HarnessProcess.DeskWaiting {
		return nil
	}
	return executionRequestError(s.startExecution(ctx, port))
}

func (s *AgentWork) executionRequest() StartExecutionRequest {
	return StartExecutionRequest{
		RepositorySelection:    s.RepositorySelection.Clone(),
		AuthorizationSelection: s.AuthorizationSelection.Clone(),
		ThinkingLevel:          s.ThinkingLevel, PermissionMode: s.PermissionMode, WorkModel: s.WorkModel, AgentID: s.AgentID, WorkID: s.ID, ComputerID: s.ComputerID, ProjectID: s.ProjectID, Capabilities: s.CapabilitySnapshot.Clone(),
		HarnessSessionReference: s.HarnessSessionReference, ContextMode: s.HarnessProcess.ContextMode, Soul: s.HarnessProcess.Soul, PreparationBound: s.HarnessProcess.degradableStepBound(preparationFollowingSteps), WorkspaceReference: s.WorkspaceReference, InputSource: s.InputSource, ReadOnlyMounts: slices.Clone(s.ReadOnlyMounts),
		HomeBinding: s.HomeBinding, TraceAttributes: s.executionTraceAttributes(), StartingContext: s.StartingContext.clone(),
	}
}

// executionTraceAttributes carries the attributes the creating caller
// supplied, unchanged on every start, including Resume and Restart, beside
// Agent's own facts of the Work, its definition and its Project, which a
// caller attribute cannot replace. The Subject, the caller's to interpret, is
// not among them. A Work's starter or Agent Owner does not establish a
// telemetry user identity.
func (s *AgentWork) executionTraceAttributes() map[string]string {
	attributes := maps.Clone(s.TraceAttributes)
	if attributes == nil {
		attributes = make(map[string]string)
	}
	attributes["talgent.agent_work_id"] = s.ID
	attributes["talgent.agent_id"] = s.AgentID
	delete(attributes, "talgent.project_id")
	if s.ProjectID != "" {
		attributes["talgent.project_id"] = s.ProjectID
	}
	return attributes
}

// RecoverExecution advances retained execution obligations without new admission
// or context selection. Its result indicates a Domain state change to persist.
func (s *AgentWork) RecoverExecution(ctx context.Context, port ExecutionPort) (bool, error) {
	p := s.HarnessProcess
	if p == nil || p.ComputeReclaimed {
		return false, nil
	}
	if port == nil {
		return false, ErrInvalidWork
	}
	if s.processing() && s.EndRequestReference != "" {
		if p.Turn.Active {
			return false, nil
		}
		return true, s.stop(ctx, s.EndRequestReference, StopReasonCompleted, port)
	}
	if s.State == WorkFailed && !p.ended() && s.EndRequestReference != "" && s.StopRequestReference == "" {
		return true, s.RequestExecutionEnd(ctx, s.EndRequestReference, port)
	}
	if s.State == WorkStarting {
		if p.DeskWaiting {
			return false, nil
		}
		return false, executionRequestError(s.startExecution(ctx, port))
	}
	if s.State != WorkStopping && s.State != WorkClosing && s.State != WorkFailed {
		return false, nil
	}
	if p.ended() || p.Unreached {
		return false, executionRequestError(port.ReclaimCompute(ctx, s.ID))
	}
	if s.StopRequestReference == "" {
		return false, nil
	}
	// A close can race the first outbound start. Complete delivery of that
	// already admitted start so its process can be asked to exit.
	if !p.Prepared {
		if err := s.startExecution(ctx, port); err != nil {
			return false, executionRequestError(err)
		}
	}
	// A waiting timeout ended the Turn a Required Response held open; the
	// cancellation is repeated until the stop it waits for succeeds.
	if s.State == WorkStopping && s.StopReason == StopReasonWaitingTimeout && p.Turn.Active {
		if err := port.CancelRunningTurn(ctx, s.ID); err != nil {
			return false, executionRequestError(err)
		}
	}
	return false, executionRequestError(port.StopExecution(ctx, s.ID, s.StopRequestReference))
}

// ConfirmPrepared applies supplied preparation feedback for the current process
// at the supplied current time. Feedback after the start's Startup Deadline
// does not complete the start; the Work fails instead.
func (s *AgentWork) ConfirmPrepared(ctx context.Context, now time.Time, port ExecutionPort) error {
	p := s.HarnessProcess
	// An abandoned start's environment is being released; a late report
	// cannot revive it.
	if p == nil || p.Prepared || p.StartAbandoned {
		return nil
	}
	if s.State != WorkStarting && !s.processing() && s.State != WorkStopping && s.State != WorkClosing && s.State != WorkFailed {
		return nil
	}
	if failed, err := s.failLateCompletion(ctx, now, port); failed || err != nil {
		return err
	}
	p.Prepared = true
	s.confirmReadiness()
	return nil
}

// ConfirmStarted applies supplied Agent startup feedback for the current process
// at the supplied current time, under the same Startup Deadline as preparation.
func (s *AgentWork) ConfirmStarted(ctx context.Context, now time.Time, port ExecutionPort) error {
	p := s.HarnessProcess
	if p == nil || p.Started || p.StartAbandoned {
		return nil
	}
	if s.State != WorkStarting && !s.processing() && s.State != WorkStopping && s.State != WorkClosing && s.State != WorkFailed {
		return nil
	}
	if failed, err := s.failLateCompletion(ctx, now, port); failed || err != nil {
		return err
	}
	p.Started = true
	s.confirmReadiness()
	return nil
}

// failLateCompletion keeps a completion reported after the Startup Deadline
// from completing a start that is still Starting. The failure is decided before
// the late fact is applied, so its reason names the earliest step incomplete
// at the deadline.
func (s *AgentWork) failLateCompletion(ctx context.Context, now time.Time, port ExecutionPort) (bool, error) {
	if s.State != WorkStarting {
		return false, nil
	}
	if now.IsZero() {
		return false, ErrInvalidWork
	}
	return s.FailAtStartupDeadline(ctx, now, port)
}

func (s *AgentWork) confirmReadiness() {
	if s.State == WorkStarting && s.HarnessProcess.Prepared && s.HarnessProcess.Started {
		s.State = WorkWorking
	}
}

func validateStartAdmission(agentID string, model WorkModelSelection, admission StartAdmission, port ExecutionPort) error {
	if !model.Available(admission.ModelConfigurationProvided) {
		return ErrModelConfigurationWithdrawn
	}
	if !admission.DefinitionAdmitted || admission.AgentID != agentID {
		return ErrExecutionNotPermitted
	}
	if !admission.Seat.admitted() {
		return ErrCapacityUnavailable
	}
	if admission.AdmittedAt.IsZero() || port == nil {
		return ErrInvalidWork
	}
	return nil
}

func (s *AgentWork) startExecution(ctx context.Context, port ExecutionPort) error {
	if err := s.CapabilitySnapshot.Validate(); err != nil {
		return err
	}
	return port.StartExecution(ctx, s.executionRequest())
}
