package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidExecution   = errors.New("agent: invalid execution")
	ErrExecutionNotFound  = errors.New("agent: execution not found")
	ErrExecutionAdmission = errors.New("agent: the Work already has an unretired execution")
)

// ExecutionState is an AgentExecution's Lifecycle State.
type ExecutionState string

const (
	// ExecutionQueued is admitted with its Desk waiting in its Computer's
	// line, before its Startup Deadline counts.
	ExecutionQueued ExecutionState = "queued"
	// ExecutionStarting is seated, with its Startup Deadline counting while
	// its run comes up.
	ExecutionStarting ExecutionState = "starting"
	// ExecutionRunning is ready, with its process serving the Work until it
	// exits.
	ExecutionRunning ExecutionState = "running"
	// ExecutionRetired is the run of a Work whose execution ended: it deletes
	// every environment it owns and leaves its Desk.
	ExecutionRetired ExecutionState = "retired"
	// ExecutionReleased owns nothing any more.
	ExecutionReleased ExecutionState = "released"
)

// ExecutionRealization is where an execution runs, fixed at its beginning:
// a Cloud's OpenSandbox, or the Work's Managed Computer.
type ExecutionRealization string

const (
	RealizationCloud   ExecutionRealization = "cloud"
	RealizationManaged ExecutionRealization = "managed"
)

// ExecutionDesk is the execution's Desk on the Work's Computer: whether and
// when it was seated, and when it left.
type ExecutionDesk struct {
	ID       string
	SeatedAt time.Time
	LeftAt   time.Time
}

// CreationSubmission is when its environment's creation was submitted, its
// creation deadline, and whether a definite answer arrived; a definite refusal
// is the start's rejection (ExecutionOutcome.Rejected).
type CreationSubmission struct {
	SubmittedAt time.Time
	// Deadline is the submission time plus the configured creation wait, after
	// which an unanswered creation can no longer produce an environment
	// (Creation deadline).
	Deadline time.Time
	Answered bool
}

// Submitted reports whether its creation was submitted.
func (c CreationSubmission) Submitted() bool { return !c.SubmittedAt.IsZero() }

// Outstanding reports a submitted creation without an answer whose deadline
// has not passed (Outstanding creation).
func (c CreationSubmission) Outstanding(now time.Time) bool {
	return c.Submitted() && !c.Answered && now.Before(c.Deadline)
}

// LaunchedProcess is the Harness process launched into the bound environment:
// the end of its preparation, its readiness reports and its acknowledged exit.
type LaunchedProcess struct {
	ID               string
	PreparationEnded bool
	// Prepared and Started are the executor's reports that its environment
	// is prepared and the Harness started; both make the run ready.
	Prepared bool
	Started  bool
	Exited   bool
	Exit     HarnessExit
}

// ExitRequest is the Work's request that the process exit, under the Work's
// stop identity, with a Waiting Turn cancelled first when asked.
type ExitRequest struct {
	StopRequestID     string
	CancelWaitingTurn bool
}

// Requested reports whether the Work asked the process to exit.
func (r ExitRequest) Requested() bool { return r.StopRequestID != "" }

// CredentialDelivery is the credentials last delivered into the bound
// environment, by digest and earliest declared expiry, zero when none
// expires; Withdrawn once they were cleared for an exit.
type CredentialDelivery struct {
	Delivered bool
	Digest    string
	ExpiresAt time.Time
	Withdrawn bool
}

// InputSync is, on a Managed Computer, the input changes its machine has not
// received yet and when its copies were last synced.
type InputSync struct {
	Due      int64
	SyncedAt time.Time
}

// ExecutionOutcome is what the execution established about its start, each
// reported to its Work: a start failure naming the earliest incomplete
// Required Startup Step or the definite cause that keeps it from completing,
// a rejected start with its cause when one is known, an abandoned start, or
// the loss of its environment.
type ExecutionOutcome struct {
	StartFailure    string
	Rejected        bool
	RejectionReason string
	Abandoned       bool
	Lost            bool
	LossReason      string
}

// RetirementCause is how the Work's execution ended, which alone retires an
// execution (Execution retirement); a stop request is none of them.
type RetirementCause string

const (
	RetiredByExit        RetirementCause = "exited"
	RetiredByRejection   RetirementCause = "rejected"
	RetiredByAbandonment RetirementCause = "abandoned"
	RetiredByLoss        RetirementCause = "lost"
	// RetiredUnreached is a Cloud run its Work reclaims beyond reach.
	RetiredUnreached RetirementCause = "unreached"
	// RetiredFenced is a Managed run its machine fenced during a
	// Disconnection.
	RetiredFenced RetirementCause = "fenced"
	// RetiredWithdrawn is a start withdrawn while its Desk waited.
	RetiredWithdrawn RetirementCause = "withdrawn"
	// RetiredSuperseded is an execution still unretired when its Work
	// admitted a later start (Superseded execution).
	RetiredSuperseded RetirementCause = "superseded"
)

// AgentExecution is one run of a Work's Harness process on its Computer, from
// start admission until its Desk, environment and process are released. It
// decides each step from its own request and the current environment
// observation, and reports what happened to its Work.
type AgentExecution struct {
	ID     string `validate:"required,uuid"`
	WorkID string `validate:"required"`
	// StartAdmittedAt is when the start it serves was admitted; the Work's
	// current start is the one admitted then.
	StartAdmittedAt time.Time
	Realization     ExecutionRealization `validate:"oneof=cloud managed"`
	Desk            ExecutionDesk
	Launch          LaunchSpecification
	Creation        CreationSubmission
	// Environment is the one environment it serves, bound from
	// observation; empty while none is bound.
	Environment string
	// Lease is the bound Cloud environment's expiry as the environment
	// authority last returned it; zero while none is known, as for an
	// environment created without one.
	Lease   time.Time
	Process LaunchedProcess
	// PluginResults are its selected Plugins' preparation outcomes in this
	// run, at most one per Plugin, in no particular order.
	PluginResults []PluginResult
	// CheckoutResults are the outcomes of the repositories this run checked
	// out, in its LaunchSpecification's order; empty until its checkout ended.
	CheckoutResults []RepositoryCheckout
	ExitRequest     ExitRequest
	Credentials     CredentialDelivery
	Inputs          InputSync
	// StartupSetback names the cause of the latest failed attempt at one of
	// its long startup steps, which may pass; empty once none failed. A start
	// failure at its Startup Deadline carries it.
	StartupSetback string
	Outcome        ExecutionOutcome
	State           ExecutionState `validate:"oneof=queued starting running retired released"`
	Retirement      RetirementCause
	RetiredAt       time.Time
	ReleasedAt      time.Time
	Events          EventCollection
	Version         int `validate:"gte=0"`
}

// ExecutionBeginning is what Begin takes from an admitted start: the Work's
// start admission, the LaunchSpecification taken from the Work and where the
// Work's Computer realizes it.
type ExecutionBeginning struct {
	Admitted    WorkStartAdmitted
	Launch      LaunchSpecification
	Realization ExecutionRealization
}

// BeginExecution begins the AgentExecution of an admitted start, Queued while
// its Desk waits in its Computer's line and Starting once seated, its Startup
// Deadline counting from its seating (Line before deadline).
func BeginExecution(beginning ExecutionBeginning) (*AgentExecution, error) {
	admitted := beginning.Admitted
	if strings.TrimSpace(admitted.WorkID) == "" || admitted.WorkID != beginning.Launch.WorkID || admitted.StartAdmittedAt.IsZero() || strings.TrimSpace(admitted.DeskID) == "" {
		return nil, ErrInvalidExecution
	}
	execution := &AgentExecution{
		ID: uuid.Must(uuid.NewV7()).String(), WorkID: admitted.WorkID, StartAdmittedAt: admitted.StartAdmittedAt.UTC(),
		Realization: beginning.Realization, Desk: ExecutionDesk{ID: admitted.DeskID}, Launch: beginning.Launch,
		State: ExecutionQueued, Events: NewEventCollection(),
	}
	if !admitted.Waiting {
		execution.seat(execution.StartAdmittedAt)
	}
	if err := execution.Validate(); err != nil {
		return nil, err
	}
	return execution, nil
}

func (e *AgentExecution) Validate() error {
	if err := validation.Struct(e); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidExecution, err)
	}
	if e.StartAdmittedAt.IsZero() || e.Launch.WorkID != e.WorkID {
		return ErrInvalidExecution
	}
	if e.State == ExecutionQueued && !e.Desk.SeatedAt.IsZero() {
		return ErrInvalidExecution
	}
	if e.Retired() != !e.RetiredAt.IsZero() || (e.State == ExecutionReleased) != !e.ReleasedAt.IsZero() {
		return ErrInvalidExecution
	}
	return nil
}

// Seat records that its Desk was seated at seatedAt, from which its Startup
// Deadline counts, transitioning from Queued to Starting. A repeated seating
// changes nothing.
func (e *AgentExecution) Seat(seatedAt time.Time) (bool, error) {
	if seatedAt.IsZero() {
		return false, ErrInvalidExecution
	}
	if e.State != ExecutionQueued {
		return false, nil
	}
	e.seat(seatedAt.UTC())
	return true, nil
}

func (e *AgentExecution) seat(seatedAt time.Time) {
	e.Desk.SeatedAt = seatedAt
	e.Launch.PreparationBound = e.PreparationBound()
	e.State = ExecutionStarting
}

// Queued reports whether its Desk still waits in its Computer's line.
func (e *AgentExecution) Queued() bool { return e.State == ExecutionQueued }

// Retired reports whether it stopped wanting its run.
func (e *AgentExecution) Retired() bool {
	return e.State == ExecutionRetired || e.State == ExecutionReleased
}

// Serves reports whether it is the execution of the Work's start admitted at
// startAdmittedAt, so a request about an earlier start, delivered late, never
// reaches the execution of a later one. Times compare as they are kept, to
// the millisecond.
func (e *AgentExecution) Serves(startAdmittedAt time.Time) bool {
	return servesStart(e.StartAdmittedAt, startAdmittedAt)
}

func servesStart(admitted, start time.Time) bool {
	return !admitted.IsZero() && !start.IsZero() && admitted.Truncate(time.Millisecond).Equal(start.Truncate(time.Millisecond))
}

// HoldsDesk reports whether it still holds the Desk's room on its Computer.
func (e *AgentExecution) HoldsDesk(deskID string) bool {
	return deskID != "" && e.Desk.ID == deskID && e.Desk.LeftAt.IsZero() && e.State != ExecutionReleased
}

// RequestExit records the Work's request that its process exit, with a
// Waiting Turn cancelled first when asked (Stop only asks, Timeout cancels
// first). It never retires the execution (Stop is not retirement); a
// repeated request keeps the original stop identity.
func (e *AgentExecution) RequestExit(requested WorkStopRequested) (bool, error) {
	if strings.TrimSpace(requested.StopRequestID) == "" || requested.WorkID != e.WorkID {
		return false, ErrInvalidExecution
	}
	if e.Retired() || !e.Serves(requested.StartAdmittedAt) {
		return false, nil
	}
	changed := false
	if !e.ExitRequest.Requested() {
		e.ExitRequest.StopRequestID, changed = requested.StopRequestID, true
	}
	if requested.CancelWaitingTurn && !e.ExitRequest.CancelWaitingTurn {
		e.ExitRequest.CancelWaitingTurn, changed = true, true
	}
	return changed, nil
}

// Retire stops wanting its run (Execution retirement): once its Work's
// execution ended, as its Work's WorkComputeReleaseRequested says, by its
// exit acknowledged, its start rejected or abandoned, its environment lost,
// or its Work reclaiming an unreached run, its machine fencing it, or its
// waiting start withdrawn; or once its Work admitted a later start while it
// was unretired, as that start's WorkStartAdmitted says (Superseded
// execution). A cause its own facts contradict, or one about its own or an
// earlier start, retires nothing. A repeated retirement changes nothing.
func (e *AgentExecution) Retire(cause Event, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrInvalidExecution
	}
	var retirement RetirementCause
	switch c := cause.(type) {
	case WorkComputeReleaseRequested:
		if c.WorkID != e.WorkID {
			return false, ErrInvalidExecution
		}
		// A request about an earlier start, delivered late, never retires
		// the execution of a later one.
		if e.Retired() || !e.Serves(c.StartAdmittedAt) || !e.ended(c.Cause) {
			return false, nil
		}
		retirement = c.Cause
	case WorkStartAdmitted:
		if c.WorkID != e.WorkID || c.StartAdmittedAt.IsZero() {
			return false, ErrInvalidExecution
		}
		if e.Retired() || !e.StartAdmittedAt.Truncate(time.Millisecond).Before(c.StartAdmittedAt.Truncate(time.Millisecond)) {
			return false, nil
		}
		retirement = RetiredSuperseded
	default:
		return false, ErrInvalidExecution
	}
	e.State, e.Retirement, e.RetiredAt = ExecutionRetired, retirement, now.UTC()
	return true, nil
}

// ended reports whether its facts agree that the Work's execution ended by
// cause. An unreached, fenced or withdrawn run ends by its Work's decision.
func (e *AgentExecution) ended(cause RetirementCause) bool {
	switch cause {
	case RetiredByExit:
		return e.Process.Exited
	case RetiredByRejection:
		return e.Outcome.Rejected
	case RetiredByAbandonment:
		return e.Outcome.Abandoned
	case RetiredByLoss:
		return e.Outcome.Lost
	case RetiredUnreached, RetiredFenced, RetiredWithdrawn:
		return true
	default:
		return false
	}
}

// RecordDeskLeft records that its Desk left its Computer, once it retired.
func (e *AgentExecution) RecordDeskLeft(now time.Time) bool {
	if !e.Retired() || !e.Desk.LeftAt.IsZero() || now.IsZero() {
		return false
	}
	e.Desk.LeftAt = now.UTC()
	return true
}

// ConfirmRelease confirms that it owns no environment, as observed now, not
// even one still ending (Ending environment), left its Desk and has no
// outstanding creation (Outstanding creation), transitioning from Retired to
// Released without a recorded environment identity (Release confirmation);
// its Work's compute release is then confirmed (Released compute).
func (e *AgentExecution) ConfirmRelease(observation EnvironmentObservation, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrInvalidExecution
	}
	if e.State != ExecutionRetired || e.Desk.LeftAt.IsZero() || !e.computeEnded(observation, now) {
		return false, nil
	}
	e.State, e.ReleasedAt = ExecutionReleased, now.UTC()
	e.record(ExecutionReleasedEvent{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
	return true, nil
}

// computeEnded reports that it owns no environment as observed now, not even
// one still ending, and no creation of its is outstanding.
func (e *AgentExecution) computeEnded(observation EnvironmentObservation, now time.Time) bool {
	return len(observation.Environments) == 0 && !e.Creation.Outstanding(now)
}

func (e *AgentExecution) record(event Event) {
	if e.Events == nil {
		e.Events = NewEventCollection()
	}
	e.Events.Add(event)
}
