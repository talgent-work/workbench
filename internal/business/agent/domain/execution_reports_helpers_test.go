package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// These helpers stand in for the AgentExecution of a Work's current start:
// they apply the reports it makes and read the intents the Work passes it.

func currentStart(work *domain.AgentWork) domain.ExecutionReady {
	return domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince}
}

func report(t *testing.T, work *domain.AgentWork, report domain.Event) {
	t.Helper()
	_, err := work.ApplyExecutionReport(report)
	require.NoError(t, err)
}

// ready reports the current start's run ready.
func ready(t *testing.T, work *domain.AgentWork) {
	t.Helper()
	report(t, work, currentStart(work))
}

// exited reports the acknowledged exit of the current start's process.
func exited(t *testing.T, work *domain.AgentWork, exit domain.HarnessExit) {
	t.Helper()
	s := currentStart(work)
	report(t, work, domain.ExecutionExited{WorkID: s.WorkID, StartAdmittedAt: s.StartAdmittedAt, Exit: exit, Ready: work.HarnessProcess.Ready})
}

// released reports the current start's execution released.
func released(t *testing.T, work *domain.AgentWork) {
	t.Helper()
	s := currentStart(work)
	report(t, work, domain.ExecutionReleasedEvent{WorkID: s.WorkID, StartAdmittedAt: s.StartAdmittedAt})
}

// lost reports the current start's environment lost.
func lost(t *testing.T, work *domain.AgentWork, reason string) {
	t.Helper()
	s := currentStart(work)
	report(t, work, domain.ExecutionEnvironmentLost{WorkID: s.WorkID, StartAdmittedAt: s.StartAdmittedAt, Reason: reason})
}

// rejected reports the current start's creation refused.
func rejected(t *testing.T, work *domain.AgentWork) {
	t.Helper()
	s := currentStart(work)
	report(t, work, domain.ExecutionStartRejected{WorkID: s.WorkID, StartAdmittedAt: s.StartAdmittedAt})
}

// abandoned reports the current start abandoned.
func abandoned(t *testing.T, work *domain.AgentWork) {
	t.Helper()
	s := currentStart(work)
	report(t, work, domain.ExecutionStartAbandoned{WorkID: s.WorkID, StartAdmittedAt: s.StartAdmittedAt})
}

// missedDeadline reports the current start's Startup Deadline missed.
func missedDeadline(t *testing.T, work *domain.AgentWork, reason string) {
	t.Helper()
	s := currentStart(work)
	report(t, work, domain.ExecutionStartFailed{WorkID: s.WorkID, StartAdmittedAt: s.StartAdmittedAt, Reason: reason})
}

// intent is the Work's current intent of type T for its execution.
func intent[T domain.Event](work *domain.AgentWork) (T, bool) {
	for _, candidate := range work.Intents() {
		if found, ok := candidate.(T); ok {
			return found, true
		}
	}
	var none T
	return none, false
}

// releaseCause is why the Work asks its execution to retire, empty while it
// does not.
func releaseCause(work *domain.AgentWork) domain.RetirementCause {
	requested, _ := intent[domain.WorkComputeReleaseRequested](work)
	return requested.Cause
}

// stopRequest is the stop identity under which the Work asks its process to
// exit, empty while it does not.
func stopRequest(work *domain.AgentWork) string {
	requested, _ := intent[domain.WorkStopRequested](work)
	return requested.StopRequestID
}

// recorded drains the Work's events and lists those of type T.
func recorded[T domain.Event](work *domain.AgentWork) []T {
	var found []T
	for _, event := range work.Events.Drain() {
		if typed, ok := event.(T); ok {
			found = append(found, typed)
		}
	}
	return found
}

// passed are the intents a Work recorded for its executions since its events
// were last drained, as its Policies pass them on.
type passed struct {
	// starts counts the admitted starts.
	starts int
	// stops are the stop identities its process was asked to exit under.
	stops []string
	// cancels counts the exit requests that cancel a Waiting Turn first.
	cancels int
	// releases are the causes its executions were asked to retire for.
	releases []domain.RetirementCause
}

// intentsOf reads the intents the Work recorded, leaving its events in place.
func intentsOf(work *domain.AgentWork) passed {
	if work == nil {
		return passed{}
	}
	events := work.Events.Drain()
	var result passed
	for _, event := range events {
		work.Events.Add(event)
		switch e := event.(type) {
		case domain.WorkStartAdmitted:
			result.starts++
		case domain.WorkStopRequested:
			result.stops = append(result.stops, e.StopRequestID)
			if e.CancelWaitingTurn {
				result.cancels++
			}
		case domain.WorkComputeReleaseRequested:
			result.releases = append(result.releases, e.Cause)
		}
	}
	return result
}

// executionPort follows one Work and reads the intents it passed to its
// executions, as its Policies would carry them.
type executionPort struct{ work *domain.AgentWork }

func follow(work *domain.AgentWork) *executionPort { return &executionPort{work: work} }

// starts counts the admitted starts.
func (p *executionPort) starts() int { return intentsOf(p.work).starts }

// launch is what the Work's latest start launches with.
func (p *executionPort) launch() domain.LaunchSpecification { return p.work.LaunchSpecification() }

// stops are the stop identities its process was asked to exit under.
func (p *executionPort) stops() []string { return intentsOf(p.work).stops }

// cancels counts the exit requests that cancel a Waiting Turn first.
func (p *executionPort) cancels() int { return intentsOf(p.work).cancels }

// reclaims counts the retirements asked for that release compute.
func (p *executionPort) reclaims() int {
	count := 0
	for _, cause := range intentsOf(p.work).releases {
		if cause != domain.RetiredFenced {
			count++
		}
	}
	return count
}

// retires counts the retirements asked for a run its Managed Computer fenced,
// which release no compute.
func (p *executionPort) retires() int {
	count := 0
	for _, cause := range intentsOf(p.work).releases {
		if cause == domain.RetiredFenced {
			count++
		}
	}
	return count
}
