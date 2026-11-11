package domain

import (
	"strings"
	"time"
)

// StartupDeadline bounds one start of a Work, counted from its run being
// seated on its Computer; waiting in its Computer's line comes before it.
// Required Startup Steps fail the start when they cannot complete by then;
// other steps degrade. Repository checkout and Plugin preparation have no
// limit of their own.
const StartupDeadline = 15 * time.Minute

// Degradable Startup Steps end early enough for the Required Startup Steps
// that follow them. Plugin preparation and then repository checkout run inside
// the Work's environment before the Harness starts, so both are followed by
// the Harness start.
const preparationFollowingSteps = 2 * time.Minute

// abandonmentMargin keeps a launch begun just before the Startup Deadline
// from being taken for abandonment while its process is recorded.
const abandonmentMargin = time.Minute

// Failure reasons name the earliest Required Startup Step still incomplete.
const (
	startupEnvironmentIncomplete = "Startup Deadline reached before the execution environment was ready."
	startupHarnessIncomplete     = "Startup Deadline reached before the Harness started."
	startupCredentialsIncomplete = "Startup Deadline reached before the Work's Identity Token and model credentials were delivered."
)

// startRejectedReason is the failure of a start whose environment could not
// be created (Rejected start).
const startRejectedReason = "The execution environment could not be created for this start."

// StartupDeadlineAt is when its Startup Deadline passes, counted from its
// seating; a Queued execution has none yet.
func (e *AgentExecution) StartupDeadlineAt() time.Time {
	if e.Desk.SeatedAt.IsZero() {
		return time.Time{}
	}
	return e.Desk.SeatedAt.Add(StartupDeadline)
}

// PreparationBound is when its in-environment preparation (Plugin
// installation, then repository checkout) degrades: the Startup Deadline,
// less the time the Harness start that follows needs (Degradable step bound).
func (e *AgentExecution) PreparationBound() time.Time {
	if e.Desk.SeatedAt.IsZero() {
		return time.Time{}
	}
	return e.Desk.SeatedAt.Add(StartupDeadline - preparationFollowingSteps)
}

// Ready reports whether both readiness reports of its launched process
// arrived.
func (e *AgentExecution) Ready() bool { return e.Process.Prepared && e.Process.Started }

// pastDeadline reports a seated start whose Startup Deadline passed at now.
func (e *AgentExecution) pastDeadline(now time.Time) bool {
	deadline := e.StartupDeadlineAt()
	return !deadline.IsZero() && !now.Before(deadline)
}

// AdmitEnvironmentAction admits a launch or a credential delivery only into
// its bound environment while it still wants its run (Bound environment
// only, Exit request ends delivery, Abandonment is final). Application asks
// it again between the remote calls of such an action, so an exit request
// admitted meanwhile stops what follows.
func (e *AgentExecution) AdmitEnvironmentAction(environmentID string) bool {
	return environmentID != "" && environmentID == e.Environment && e.wantsRun()
}

// wantsRun reports whether it still wants its run: unretired, without an
// exit request from its Work, and without its start failed, rejected or
// abandoned, its environment lost or its process exited.
func (e *AgentExecution) wantsRun() bool {
	o := e.Outcome
	return !e.Retired() && !e.ExitRequest.Requested() && o.StartFailure == "" && !o.Rejected && !o.Abandoned && !o.Lost && !e.Process.Exited
}

// RecordSubmission records that its environment's creation is submitted now,
// before it is sent, so it is never submitted twice (Single creation), with
// its creation deadline: now plus the configured creation wait (Creation
// deadline), its realization's wait. An execution that submitted already, or
// no longer wants its run, submits nothing.
func (e *AgentExecution) RecordSubmission(now time.Time, waits CreationWaits) (bool, error) {
	wait := waits.of(e.Realization)
	if now.IsZero() || wait.Validate() != nil {
		return false, ErrInvalidExecution
	}
	if e.Creation.Submitted() || e.State == ExecutionQueued || !e.wantsRun() {
		return false, nil
	}
	now = now.UTC()
	e.Creation = CreationSubmission{SubmittedAt: now, Deadline: now.Add(time.Duration(wait))}
	return true, nil
}

// CreationAnswer is the definite answer to its environment's creation: the
// created environment, or its refusal or failure.
type CreationAnswer struct {
	EnvironmentID string
	Refused       bool
}

// RecordCreationAnswer applies its creation's definite answer, which ends its
// outstanding creation whatever else it decides (Outstanding creation): a
// created environment is bound if none is yet (Single creation); a refusal or
// a failure while nothing is bound rejects the start (Creation refusal). A
// refusal after binding changes nothing more: the bound environment's loss is
// observed instead.
func (e *AgentExecution) RecordCreationAnswer(answer CreationAnswer) (bool, error) {
	if (answer.EnvironmentID == "") == !answer.Refused {
		return false, ErrInvalidExecution
	}
	answered := !e.Creation.Answered
	e.Creation.Answered = true
	if e.Retired() || e.Environment != "" || e.Outcome.Rejected || e.Outcome.Abandoned {
		return answered, nil
	}
	if !answer.Refused {
		e.Environment, e.StartupSetback = answer.EnvironmentID, ""
		return true, nil
	}
	e.reject("")
	return true, nil
}

// reject rejects its start, naming the cause when one is known (Creation
// refusal).
func (e *AgentExecution) reject(cause string) {
	e.Outcome.Rejected, e.Outcome.RejectionReason = true, cause
	e.record(ExecutionStartRejected{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Reason: cause})
}

// RecordCredentialDelivery records the credentials delivered into its bound
// environment, by digest and earliest declared expiry, while it admits the
// delivery (Admit environment action).
func (e *AgentExecution) RecordCredentialDelivery(delivery CredentialDelivery) bool {
	if !e.AdmitEnvironmentAction(e.Environment) {
		return false
	}
	delivery.Delivered, delivery.Withdrawn = true, false
	if e.Credentials == delivery {
		return false
	}
	e.Credentials = delivery
	return true
}

// MarkCredentialsChanged records that an authorization it uses changed, so
// its delivered credentials are replaced (Credential replacement).
func (e *AgentExecution) MarkCredentialsChanged() bool {
	if e.Retired() || !e.Credentials.Delivered {
		return false
	}
	e.Credentials.ExpiresAt = staleCredentials
	return true
}

// staleCredentials is the expiry of a delivery that is due at once.
var staleCredentials = time.UnixMilli(1).UTC()

// RecordCredentialsWithdrawn records that its delivered credentials were
// cleared once its Work asked the process to exit.
func (e *AgentExecution) RecordCredentialsWithdrawn() bool {
	if !e.ExitRequest.Requested() || e.Credentials.Withdrawn {
		return false
	}
	e.Credentials.Withdrawn = true
	return true
}

// RecordLaunch records the Harness process launched into its bound
// environment while it admits the launch (Admit environment action); a
// second process is never recorded.
func (e *AgentExecution) RecordLaunch(processID string) (bool, error) {
	if strings.TrimSpace(processID) == "" {
		return false, ErrInvalidExecution
	}
	if e.Process.ID != "" || !e.AdmitEnvironmentAction(e.Environment) {
		return false, nil
	}
	e.Process.ID, e.StartupSetback = processID, ""
	return true, nil
}

// ReadinessReport is the executor's report about its launched process: its
// environment is prepared, the Harness started, or both.
type ReadinessReport struct {
	Prepared bool
	Started  bool
}

// ConfirmReady applies a readiness report of its launched process at now,
// confirming the run ready once both arrived before its Startup Deadline,
// transitioning from Starting to Running (Execution readiness). A report at
// or after the deadline completes nothing: the start fails, naming the
// earliest step incomplete at the deadline (Startup Deadline is a hard
// cut-off, No late Working).
func (e *AgentExecution) ConfirmReady(report ReadinessReport, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrInvalidExecution
	}
	if e.State != ExecutionStarting || e.Process.ID == "" || e.Process.Exited {
		return false, nil
	}
	if e.pastDeadline(now) {
		return e.failStart(), nil
	}
	changed := false
	if report.Prepared && !e.Process.Prepared {
		e.Process.Prepared, changed = true, true
	}
	if report.Started && !e.Process.Started {
		e.Process.Started, changed = true, true
	}
	if e.Ready() {
		e.State = ExecutionRunning
		e.record(ExecutionReady{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
	}
	return changed, nil
}

// failStart records that its start missed its Startup Deadline, naming the
// earliest incomplete Required Startup Step (Failure names the step).
func (e *AgentExecution) failStart() bool {
	if e.Outcome.StartFailure != "" || e.State != ExecutionStarting {
		return false
	}
	reason := startupHarnessIncomplete
	switch {
	case e.Environment == "":
		reason = startupEnvironmentIncomplete
	case !e.Credentials.Delivered:
		reason = startupCredentialsIncomplete
	case !e.Process.Prepared:
		reason = startupEnvironmentIncomplete
	}
	if e.StartupSetback != "" {
		// The last setback is why the step did not complete.
		reason += " Last error: " + e.StartupSetback + "."
	}
	e.Outcome.StartFailure = reason
	e.record(ExecutionStartFailed{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Reason: reason})
	return true
}

// RecordExit acknowledges the exit fact that names its launched process
// (Lifecycle observation); one naming another process changes nothing
// (Foreign exit), and neither does a repeated one. It reports whether the
// fact named its process, so the fact is acknowledged.
func (e *AgentExecution) RecordExit(processID string, exit HarnessExit) (current, changed bool) {
	if strings.TrimSpace(processID) == "" || processID != e.Process.ID {
		return false, false
	}
	if e.Process.Exited || e.Retired() {
		return true, false
	}
	e.Process.Exited, e.Process.Exit = true, exit
	e.record(e.exitReport())
	return true, true
}
