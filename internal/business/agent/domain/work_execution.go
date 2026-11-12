package domain

import (
	"errors"
	"maps"
	"time"
)

// ErrStartRequestSpent refuses a start request whose Desk the Work's ended
// start took: one request identity admits at most one start (Unique start
// request).
var ErrStartRequestSpent = errors.New("agent: the start request already admitted a start")

// Resume admits a continuation under its Computer's admission answer. A
// repeated request, whose Desk the current start holds, returns that same
// admission and changes nothing (Unique start request).
func (s *AgentWork) Resume(admission StartAdmission) error {
	if s.StartHoldsDesk(admission.Seat.DeskID) {
		return nil
	}
	if err := s.AdmitResume(); err != nil {
		return err
	}
	return s.begin(admission, ContextContinue)
}

// Restart admits an explicit retry the same way, a repeated request included.
func (s *AgentWork) Restart(admission StartAdmission) error {
	if s.StartHoldsDesk(admission.Seat.DeskID) {
		return nil
	}
	if err := s.AdmitRestart(); err != nil {
		return err
	}
	return s.begin(admission, ContextRecover)
}

// StartHoldsDesk reports whether the Work's current start holds the Desk: it
// took that Desk, and its compute, with which the Desk leaves, is not yet
// reclaimed. A start request derives its Desk from its identity, so a request
// whose Desk the current start holds is a repeat of the admitted one. A late
// repeat, whose Desk already left, is not recognized; its Computer refuses
// the Desk that left, so it starts nothing.
func (s *AgentWork) StartHoldsDesk(deskID string) bool {
	p := s.HarnessProcess
	return deskID != "" && p != nil && p.DeskID == deskID && !p.ComputeReclaimed
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
	if p := s.HarnessProcess; p != nil && admission.Seat.DeskID != "" && p.DeskID == admission.Seat.DeskID {
		return ErrStartRequestSpent
	}
	// A Work stays on its Computer: every run is admitted there.
	if admission.Seat.ComputerID != s.ComputerID {
		return ErrExecutionNotPermitted
	}
	if err := s.admitNewRun(); err != nil {
		return err
	}
	// Each start takes the current Plugin release declarations it was supplied
	// (Start upgrade).
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
	s.spendReachResume()
	s.State = WorkStarting
	s.admitStart()
	return nil
}

// admitStart records the admitted start, whose AgentExecution begins with
// its Desk (Begin execution).
func (s *AgentWork) admitStart() {
	s.record(s.startRequest())
}

func (s *AgentWork) startRequest() WorkStartAdmitted {
	p := s.HarnessProcess
	return WorkStartAdmitted{WorkID: s.ID, StartAdmittedAt: p.StartingSince, DeskID: p.DeskID, Waiting: p.DeskWaiting}
}

// LaunchSpecification is what the current start's run launches with, taken
// from the Work: its context, capabilities, bindings, the Soul it took up,
// the repositories it selected and those still to check out.
func (s *AgentWork) LaunchSpecification() LaunchSpecification {
	return LaunchSpecification{
		RepositorySelection:    s.RepositorySelection.Clone(),
		RemainingRepositories:  s.RemainingRepositories(),
		AuthorizationSelection: s.AuthorizationSelection.Clone(),
		ThinkingLevel:          s.ThinkingLevel, PermissionMode: s.PermissionMode, WorkModel: s.WorkModel, AgentID: s.AgentID, WorkID: s.ID, ComputerID: s.ComputerID, ProjectID: s.ProjectID, Subject: s.Subject, Capabilities: s.CapabilitySnapshot.Clone(),
		HarnessSessionReference: s.HarnessSessionReference, ContextMode: s.HarnessProcess.ContextMode, Soul: s.HarnessProcess.Soul, LocatedLayout: s.LocatedLayout.Clone(),
		AgentHarness: s.AgentHarnessReference, RepresentedPerson: s.RepresentedPerson, OwningApplication: s.OwningApplication, WorkLabels: s.WorkLabels.Map(), TraceAttributes: s.executionTraceAttributes(), StartingContext: s.StartingContext.clone(),
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

// ExecutionRequests rebuilds, from its facts, the execution requests its current start still
// holds for its AgentExecution, so one whose event was lost is passed again
// (Begin execution, Request process exit, Retire execution).
func (s *AgentWork) ExecutionRequests() []Event {
	p := s.HarnessProcess
	if p == nil {
		return nil
	}
	var requests []Event
	if !p.StartWithdrawn && !p.ComputeReclaimed {
		requests = append(requests, s.startRequest())
	}
	if stop, ok := s.exitRequest(); ok {
		requests = append(requests, stop)
	}
	if release, ok := s.retirementRequest(); ok {
		requests = append(requests, release)
	}
	return requests
}

// exitRequest is the request that the current start's process exit, held
// while a stop is admitted and the process has not ended.
func (s *AgentWork) exitRequest() (WorkStopRequested, bool) {
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

// retirementRequest is the request that the current start's execution retire,
// held once the Work's execution ended.
func (s *AgentWork) retirementRequest() (WorkComputeReleaseRequested, bool) {
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
	if release, ok := s.retirementRequest(); ok {
		s.record(release)
	}
}

// requestStop records the admitted stop's request that the process exit
// (Request process exit).
func (s *AgentWork) requestStop() {
	if stop, ok := s.exitRequest(); ok {
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
