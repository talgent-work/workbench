package domain

import (
	"slices"
	"time"
)

// ObservedEnvironment is one environment carrying the execution's ownership
// as its environment authority shows it now.
type ObservedEnvironment struct {
	ID      string
	Running bool
	// LeaseExpiresAt is when the authority deletes it unless renewed; zero
	// when it was created without an expiry.
	LeaseExpiresAt time.Time
}

// EnvironmentObservation is what the environment authority and Dispatcher
// show of an execution now. A temporary observation failure is never an
// observation (Temporary observation failure): only a complete one is
// supplied.
type EnvironmentObservation struct {
	// Environments carry the execution's ownership; a bound one appears
	// exactly while it exists.
	Environments []ObservedEnvironment
	// Removed is the authority's confirmation that the Work's Managed
	// Computer was removed, with every environment on it (Removal is loss);
	// LossReason explains a loss when the authority knows why.
	Removed    bool
	LossReason string
	// Attached reports that Dispatcher reads the launched process at the
	// bound environment.
	Attached bool
	// ModelDigest identifies the model key a delivery would carry now; empty
	// when none is known.
	ModelDigest string
	// InputsRefresh is how long a Managed Computer's synced input copies stay
	// current (Input copy sync).
	InputsRefresh time.Duration
}

func (o EnvironmentObservation) find(id string) (ObservedEnvironment, bool) {
	i := slices.IndexFunc(o.Environments, func(e ObservedEnvironment) bool { return e.ID == id })
	if i < 0 {
		return ObservedEnvironment{}, false
	}
	return o.Environments[i], true
}

// ExecutionStepKind is one step an AgentExecution decides and Application
// fulfils.
type ExecutionStepKind string

const (
	// StepSubmitCreation submits its environment's creation, once, and
	// waits for its answer, which may take minutes.
	StepSubmitCreation ExecutionStepKind = "submit_creation"
	// StepDeleteEnvironment deletes one environment it owns but does not keep.
	StepDeleteEnvironment ExecutionStepKind = "delete_environment"
	// StepDeliverCredentials delivers or replaces its bound environment's
	// credentials.
	StepDeliverCredentials ExecutionStepKind = "deliver_credentials"
	// StepWithdrawCredentials clears them once the Work asked its process to
	// exit, while the process may still report its exit.
	StepWithdrawCredentials ExecutionStepKind = "withdraw_credentials"
	// StepPrepare prepares its environment (Plugins, then repository
	// checkout), then launches the Harness and connects it to Dispatcher.
	StepPrepare ExecutionStepKind = "prepare"
	// StepConnect hands its launched process to Dispatcher again.
	StepConnect ExecutionStepKind = "connect"
	// StepSignalExit asks its launched process to exit, cancelling a Waiting
	// Turn first when the Work asked.
	StepSignalExit ExecutionStepKind = "signal_exit"
	// StepSyncInputs syncs a Managed Computer's input copies.
	StepSyncInputs ExecutionStepKind = "sync_inputs"
	// StepLeaveDesk reports its Desk's departure to its Computer.
	StepLeaveDesk ExecutionStepKind = "leave_desk"
	// StepRenewLease renews its bound environment's lease to the step's
	// expiry.
	StepRenewLease ExecutionStepKind = "renew_lease"
)

// ExecutionStep is one decided step; EnvironmentID names the environment of
// a deletion, and LeaseExpiresAt the expiry a lease renewal asks for.
type ExecutionStep struct {
	Kind           ExecutionStepKind
	EnvironmentID  string
	LeaseExpiresAt time.Time
}

// ExecutionDecision is what Reconcile decided: the steps to take now, and
// whether it changed the execution's facts.
type ExecutionDecision struct {
	Steps   []ExecutionStep
	Changed bool
}

// Reconcile decides its next steps from its own facts, the environments
// carrying its ownership as observed now, the lease terms the platform is
// configured with and the current time. It binds the
// first observed environment while none is bound and deletes every other
// (Bound environment), deletes everything once retired (Retired
// environments), submits its creation once (Single creation), and records,
// for its Work, a missed Startup Deadline (Required Startup Steps), an
// abandoned start (Abandoned start) or a lost environment (Environment loss).
// It renews its bound environment's lease before it runs out (Environment
// lease).
func (e *AgentExecution) Reconcile(observation EnvironmentObservation, lease LeaseTerms, now time.Time) (ExecutionDecision, error) {
	if now.IsZero() {
		return ExecutionDecision{}, ErrInvalidExecution
	}
	before := e.facts()
	steps := e.decide(observation, lease, now)
	return ExecutionDecision{Steps: steps, Changed: e.facts() != before}, nil
}

func (e *AgentExecution) decide(observation EnvironmentObservation, lease LeaseTerms, now time.Time) []ExecutionStep {
	if e.Retired() {
		return e.release(observation)
	}
	if observation.Removed {
		e.lose(observation.LossReason)
		return nil
	}
	if e.State == ExecutionQueued {
		return nil
	}
	if e.Outcome.Rejected || e.Outcome.Lost {
		// Its end is reported; its Work retires it.
		return nil
	}
	if !e.Creation.Submitted {
		return e.submit(observation, now)
	}
	steps := e.bind(observation)
	if e.Outcome.Lost {
		return nil
	}
	e.judgeStartup(now)
	if e.Environment == "" {
		return steps
	}
	bound, _ := observation.find(e.Environment)
	steps = append(steps, e.leaseSteps(bound, lease, now)...)
	if e.Outcome.Abandoned || !bound.Running {
		return steps
	}
	if e.ExitRequest.Requested() {
		return append(steps, e.exitSteps()...)
	}
	return append(steps, e.runSteps(observation, now)...)
}

// submit decides the creation of an execution that has not submitted one. A
// start whose Work asked it to exit before anything was created never can
// launch, and is abandoned at once.
func (e *AgentExecution) submit(observation EnvironmentObservation, now time.Time) []ExecutionStep {
	if e.ExitRequest.Requested() {
		e.abandon()
		return nil
	}
	if e.pastDeadline(now) {
		e.failStart()
		e.abandon()
		return nil
	}
	return []ExecutionStep{{Kind: StepSubmitCreation}}
}

// bind keeps the bound environment and deletes every other one it owns; with
// none bound, it binds the first observed one, running ones first, whatever
// its creation's answer (Single creation). A bound environment no longer
// observed is lost (Environment loss).
func (e *AgentExecution) bind(observation EnvironmentObservation) []ExecutionStep {
	if e.Environment != "" {
		if _, found := observation.find(e.Environment); !found {
			e.lose(observation.LossReason)
			return nil
		}
	} else if len(observation.Environments) > 0 && !e.Outcome.Abandoned {
		first := observation.Environments[0]
		if i := slices.IndexFunc(observation.Environments, func(o ObservedEnvironment) bool { return o.Running }); i >= 0 {
			first = observation.Environments[i]
		}
		e.Environment = first.ID
	}
	var steps []ExecutionStep
	for _, observed := range observation.Environments {
		if observed.ID != e.Environment {
			steps = append(steps, ExecutionStep{Kind: StepDeleteEnvironment, EnvironmentID: observed.ID})
		}
	}
	return steps
}

// judgeStartup fails a start still Starting at its Startup Deadline, and
// abandons it once it can no longer launch a Harness process.
func (e *AgentExecution) judgeStartup(now time.Time) {
	if e.State != ExecutionStarting || !e.pastDeadline(now) {
		return
	}
	e.failStart()
	if e.Process.ID == "" && !now.Before(e.StartupDeadlineAt().Add(abandonmentMargin)) {
		e.abandon()
	}
}

// exitSteps clear the credentials of a run its Work asked to exit and ask its
// launched process to exit; a start that launched none launches none now and
// is abandoned past its Startup Deadline.
func (e *AgentExecution) exitSteps() []ExecutionStep {
	var steps []ExecutionStep
	if e.Credentials.Delivered && !e.Credentials.Withdrawn {
		steps = append(steps, ExecutionStep{Kind: StepWithdrawCredentials})
	}
	if e.Process.ID != "" && !e.Process.Exited {
		steps = append(steps, ExecutionStep{Kind: StepSignalExit})
	}
	return steps
}

// runSteps bring a running bound environment's run up and keep it current:
// first delivery, then preparation with the Harness start, then its
// connection and credential replacement before expiry; a Managed Computer's
// input copies are synced beside them.
func (e *AgentExecution) runSteps(observation EnvironmentObservation, now time.Time) []ExecutionStep {
	if !e.Credentials.Delivered {
		if e.Outcome.StartFailure != "" {
			return nil
		}
		return []ExecutionStep{{Kind: StepDeliverCredentials}}
	}
	var steps []ExecutionStep
	if e.inputsDue(observation, now) {
		steps = append(steps, ExecutionStep{Kind: StepSyncInputs})
	}
	switch {
	case e.Process.ID == "":
		if e.Outcome.StartFailure == "" {
			steps = append(steps, ExecutionStep{Kind: StepPrepare})
		}
	case e.Process.Exited:
	case !observation.Attached:
		steps = append(steps, ExecutionStep{Kind: StepConnect})
	case e.credentialsDue(observation, now) && e.wantsRun():
		steps = append(steps, ExecutionStep{Kind: StepDeliverCredentials})
	}
	return steps
}

// credentialsDue reports delivered credentials past their declared expiry or
// marked changed (Credential holding, Credential replacement), or a rotated
// model key.
func (e *AgentExecution) credentialsDue(observation EnvironmentObservation, now time.Time) bool {
	if expiry := e.Credentials.ExpiresAt; !expiry.IsZero() && !now.Before(expiry) {
		return true
	}
	return observation.ModelDigest != "" && observation.ModelDigest != e.Credentials.Digest
}

// inputsDue reports a Managed Computer's input copies a change left due, or
// synced longer ago than their refresh interval (Managed input copies).
func (e *AgentExecution) inputsDue(observation EnvironmentObservation, now time.Time) bool {
	if e.Realization != RealizationManaged {
		return false
	}
	if e.Inputs.Due > 0 || e.Inputs.SyncedAt.IsZero() {
		return true
	}
	return observation.InputsRefresh > 0 && !now.Before(e.Inputs.SyncedAt.Add(observation.InputsRefresh))
}

// release deletes every environment a retired execution owns and, once none
// remains, leaves its Desk, so its room returns only after its compute is
// released (Retired environments, Desk release); a released one deletes an
// environment that appeared late.
func (e *AgentExecution) release(observation EnvironmentObservation) []ExecutionStep {
	var steps []ExecutionStep
	for _, observed := range observation.Environments {
		steps = append(steps, ExecutionStep{Kind: StepDeleteEnvironment, EnvironmentID: observed.ID})
	}
	if e.State == ExecutionRetired && e.Desk.LeftAt.IsZero() && len(observation.Environments) == 0 {
		steps = append(steps, ExecutionStep{Kind: StepLeaveDesk})
	}
	return steps
}

func (e *AgentExecution) abandon() {
	if e.Outcome.Abandoned || e.Process.ID != "" {
		return
	}
	e.Outcome.Abandoned = true
	e.record(ExecutionStartAbandoned{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
}

func (e *AgentExecution) lose(reason string) {
	if e.Outcome.Lost {
		return
	}
	e.Outcome.Lost, e.Outcome.LossReason = true, reason
	e.record(ExecutionEnvironmentLost{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Reason: reason})
}

// executionFacts are the facts Reconcile may change, compared to tell whether
// it changed any.
type executionFacts struct {
	desk        ExecutionDesk
	creation    CreationSubmission
	environment string
	lease       time.Time
	process     LaunchedProcess
	outcome     ExecutionOutcome
	state       ExecutionState
}

func (e *AgentExecution) facts() executionFacts {
	return executionFacts{desk: e.Desk, creation: e.Creation, environment: e.Environment, lease: e.Lease, process: e.Process, outcome: e.Outcome, state: e.State}
}
