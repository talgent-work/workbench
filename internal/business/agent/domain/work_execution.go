package domain

import (
	"maps"
	"slices"
	"time"
)

func (s *AgentWork) Resume(admission StartAdmission) error {
	if err := s.AdmitResume(); err != nil {
		return err
	}
	return s.begin(admission, ContextContinue)
}

func (s *AgentWork) Restart(admission StartAdmission) error {
	if err := s.AdmitRestart(); err != nil {
		return err
	}
	return s.begin(admission, ContextRecover)
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

func (s *AgentWork) begin(admission StartAdmission, mode ContextMode) error {
	if err := validateStartAdmission(s.AgentID, s.WorkModel, admission); err != nil {
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
	s.admitStart()
	return nil
}

// admitStart records the admitted start, whose AgentExecution begins with
// its Desk (Begin execution).
func (s *AgentWork) admitStart() {
	s.record(s.startIntent())
}

func (s *AgentWork) startIntent() WorkStartAdmitted {
	p := s.HarnessProcess
	return WorkStartAdmitted{WorkID: s.ID, StartAdmittedAt: p.StartingSince, DeskID: p.DeskID, Waiting: p.DeskWaiting}
}

// LaunchSpecification is what the current start's run launches with, taken
// from the Work: its context, capabilities, bindings, the Soul it took up
// and the repositories it selected.
func (s *AgentWork) LaunchSpecification() LaunchSpecification {
	return LaunchSpecification{
		RepositorySelection:    s.RepositorySelection.Clone(),
		AuthorizationSelection: s.AuthorizationSelection.Clone(),
		ThinkingLevel:          s.ThinkingLevel, PermissionMode: s.PermissionMode, WorkModel: s.WorkModel, AgentID: s.AgentID, WorkID: s.ID, ComputerID: s.ComputerID, ProjectID: s.ProjectID, Capabilities: s.CapabilitySnapshot.Clone(),
		HarnessSessionReference: s.HarnessSessionReference, ContextMode: s.HarnessProcess.ContextMode, Soul: s.HarnessProcess.Soul, WorkspaceReference: s.WorkspaceReference, InputSource: s.InputSource, ReadOnlyMounts: slices.Clone(s.ReadOnlyMounts),
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

// RecoverExecution advances a retained end without new admission or context
// selection: an end that waited for its Turn stops the process once the Turn
// ended, and a Failed start's end asks its process to exit. Its result
// indicates a Domain state change to persist.
func (s *AgentWork) RecoverExecution() (bool, error) {
	p := s.HarnessProcess
	if p == nil || p.ComputeReclaimed || s.EndRequestReference == "" {
		return false, nil
	}
	if s.processing() {
		if p.Turn.Active {
			return false, nil
		}
		return true, s.stop(s.EndRequestReference, StopReasonCompleted)
	}
	if s.State == WorkFailed && !p.ended() && s.StopRequestReference == "" {
		return true, s.RequestExecutionEnd(s.EndRequestReference)
	}
	return false, nil
}

// Intents rebuilds, from its facts, the intents its current start still
// holds for its AgentExecution, so one whose event was lost is passed again
// (Begin execution, Request process exit, Retire execution).
func (s *AgentWork) Intents() []Event {
	p := s.HarnessProcess
	if p == nil {
		return nil
	}
	var intents []Event
	if !p.StartWithdrawn && !p.ComputeReclaimed {
		intents = append(intents, s.startIntent())
	}
	if stop, ok := s.stopIntent(); ok {
		intents = append(intents, stop)
	}
	if release, ok := s.releaseIntent(); ok {
		intents = append(intents, release)
	}
	return intents
}

// stopIntent is the request that the current start's process exit, held
// while a stop is admitted and the process has not ended.
func (s *AgentWork) stopIntent() (WorkStopRequested, bool) {
	p := s.HarnessProcess
	if s.StopRequestReference == "" || p.ended() || p.EnvironmentLost || p.Unreached {
		return WorkStopRequested{}, false
	}
	switch s.State {
	case WorkStopping, WorkFailed, WorkClosing:
	default:
		return WorkStopRequested{}, false
	}
	cancel := s.StopReason == StopReasonWaitingTimeout && p.Turn.Active
	return WorkStopRequested{WorkID: s.ID, StartAdmittedAt: p.StartingSince, StopRequestID: s.StopRequestReference, CancelWaitingTurn: cancel}, true
}

// releaseIntent is the request that the current start's execution retire,
// held once the Work's execution ended.
func (s *AgentWork) releaseIntent() (WorkComputeReleaseRequested, bool) {
	p := s.HarnessProcess
	var cause RetirementCause
	switch {
	case p.StartWithdrawn:
		cause = RetiredWithdrawn
	case p.StartRejected:
		cause = RetiredByRejection
	case p.StartAbandoned:
		cause = RetiredByAbandonment
	case p.EnvironmentLost:
		cause = RetiredByLoss
	case p.Fenced:
		cause = RetiredFenced
	case p.Exited:
		cause = RetiredByExit
	case p.Unreached:
		cause = RetiredUnreached
	default:
		return WorkComputeReleaseRequested{}, false
	}
	return WorkComputeReleaseRequested{WorkID: s.ID, StartAdmittedAt: p.StartingSince, Cause: cause}, true
}

// requestComputeRelease records that the current start's execution ended, so
// its AgentExecution retires (Retire execution).
func (s *AgentWork) requestComputeRelease() {
	if release, ok := s.releaseIntent(); ok {
		s.record(release)
	}
}

// requestStop records the admitted stop's request that the process exit
// (Request process exit).
func (s *AgentWork) requestStop() {
	if stop, ok := s.stopIntent(); ok {
		s.record(stop)
	}
}

func validateStartAdmission(agentID string, model WorkModelSelection, admission StartAdmission) error {
	if !model.Available(admission.ModelConfigurationProvided) {
		return ErrModelConfigurationWithdrawn
	}
	if !admission.DefinitionAdmitted || admission.AgentID != agentID {
		return ErrExecutionNotPermitted
	}
	if !admission.Seat.admitted() {
		return ErrCapacityUnavailable
	}
	if admission.AdmittedAt.IsZero() {
		return ErrInvalidWork
	}
	return nil
}
