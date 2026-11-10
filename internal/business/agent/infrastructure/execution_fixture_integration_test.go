//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

type startupAuthority struct{}

func (startupAuthority) AuthorizeWork(context.Context, string, string) error { return nil }
func (startupAuthority) AdmitRoom(context.Context) error                     { return nil }

// startedWorks reports each start a committed Work admitted, in place of
// beginning its execution.
type startedWorks struct {
	domain.AgentWorkRepository
	onStart func(domain.LaunchSpecification)
}

func (w startedWorks) Save(ctx context.Context, work *domain.AgentWork) error {
	if err := w.AgentWorkRepository.Save(ctx, work); err != nil {
		return err
	}
	w.started(work)
	return nil
}

func (w startedWorks) started(work *domain.AgentWork) {
	if work.Events == nil {
		return
	}
	for _, event := range work.Events.Drain() {
		if _, admitted := event.(domain.WorkStartAdmitted); admitted && w.onStart != nil {
			w.onStart(work.LaunchSpecification())
		}
	}
}

// runs stands in for the execution side of a Work's runs: the Policies
// between the Work and its AgentExecution run when a test asks, as the
// committed Domain Events would run them, and each begun execution is
// launched at once with the process "process" in a bound environment.
type runs struct {
	t            testing.TB
	now          func() time.Time
	executions   *execution.AgentExecutionRepository
	coordination *command.ExecutionCoordination
	feedback     *command.ApplyExecutionFeedbackHandler
}

func newRuns(t testing.TB, db *xorm.Engine, works domain.AgentWorkRepository) *runs {
	return newRunsAt(t, db, works, time.Now)
}

func newRunsAt(t testing.TB, db *xorm.Engine, works domain.AgentWorkRepository, now func() time.Time) *runs {
	return newRunsWith(t, db, works, now, &commandtest.InputBuffer{})
}

// newRunsWith reads the Work's input buffer and its process's observation
// from buffer.
func newRunsWith(t testing.TB, db *xorm.Engine, works domain.AgentWorkRepository, now func() time.Time, buffer command.DispatcherPort) *runs {
	executions := execution.NewAgentExecutionRepository(db, now)
	coordination := command.NewExecutionCoordination(now, works, commandtest.Definitions{}, executions, computertest.Checked(startupAuthority{}), buffer)
	return &runs{t: t, now: now, executions: executions, coordination: coordination,
		feedback: command.NewApplyExecutionFeedbackHandler(now, works, executions, coordination, buffer, &commandtest.InputSettlements{})}
}

// launch begins the Work's admitted start and launches its process.
func (r *runs) launch(ctx context.Context, workID string) {
	r.t.Helper()
	r.coordinate(ctx, workID)
	run, err := r.executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return
	}
	require.NoError(r.t, err)
	if run.State != domain.ExecutionStarting || run.Process.ID != "" || run.ExitRequest.Requested() {
		return
	}
	require.True(r.t, commandtest.Submit(run))
	_, err = run.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
	require.NoError(r.t, err)
	run.RecordCredentialDelivery(domain.CredentialDelivery{})
	_, err = run.RecordPreparation(domain.PreparationReport{})
	require.NoError(r.t, err)
	_, err = run.RecordLaunch("process")
	require.NoError(r.t, err)
	require.NoError(r.t, r.executions.Save(ctx, run))
}

// coordinate passes the Work's execution requests to its execution and applies the
// execution's reports to the Work.
func (r *runs) coordinate(ctx context.Context, workID string) {
	r.t.Helper()
	for range 3 {
		require.NoError(r.t, r.coordination.Coordinate(ctx, workID))
	}
}

// Handle applies one feedback about the Work's launched process, and the
// Policies it leads to; a start not launched yet is launched first.
func (r *runs) Handle(ctx context.Context, feedback command.ExecutionFeedback) error {
	r.launch(ctx, feedback.WorkID)
	if err := r.feedback.Handle(ctx, feedback); err != nil {
		return err
	}
	return r.coordination.Coordinate(ctx, feedback.WorkID)
}

// release retires the Work's execution as the Work asked and confirms its
// compute released: nothing carries its ownership any more.
func (r *runs) release(ctx context.Context, workID string) {
	r.t.Helper()
	r.coordinate(ctx, workID)
	run, err := r.executions.Latest(ctx, workID)
	require.NoError(r.t, err)
	require.True(r.t, run.Retired(), "the Work asked for its execution's retirement")
	run.RecordDeskLeft(r.now())
	_, err = run.ConfirmRelease(domain.EnvironmentObservation{}, r.now())
	require.NoError(r.t, err)
	require.NoError(r.t, r.executions.Save(ctx, run))
	r.coordinate(ctx, workID)
}

// lose observes the Work's bound environment gone.
func (r *runs) lose(ctx context.Context, workID string) {
	r.t.Helper()
	run, err := r.executions.Latest(ctx, workID)
	require.NoError(r.t, err)
	_, err = run.Reconcile(domain.EnvironmentObservation{}, domain.LeaseTerms{}, r.now())
	require.NoError(r.t, err)
	require.True(r.t, run.Outcome.Lost)
	require.NoError(r.t, r.executions.Save(ctx, run))
	r.coordinate(ctx, workID)
}

// ready reports the launched process prepared and started.
func (r *runs) ready(ctx context.Context, workID string) {
	r.t.Helper()
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(r.t, r.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: workID}))
	}
}

// exit reports the launched process exited.
func (r *runs) exit(ctx context.Context, workID string) {
	r.t.Helper()
	require.NoError(r.t, r.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: workID}))
}

// report applies one report of the Work's current execution to the Work, as
// the Policies between them would.
func report(t testing.TB, work *domain.AgentWork, kind domain.Event) {
	t.Helper()
	_, err := work.ApplyExecutionReport(kind)
	require.NoError(t, err)
}

// reportReady reports the Work's current run ready.
func reportReady(t testing.TB, work *domain.AgentWork) {
	t.Helper()
	report(t, work, domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince})
}

// reportExit reports the Work's current process exited.
func reportExit(t testing.TB, work *domain.AgentWork, exit domain.HarnessExit) {
	t.Helper()
	report(t, work, domain.ExecutionExited{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince, Exit: exit, Ready: work.HarnessProcess.Ready})
}

// reportReleased reports the Work's current execution released.
func reportReleased(t testing.TB, work *domain.AgentWork) {
	t.Helper()
	report(t, work, domain.ExecutionReleasedEvent{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince})
}

// subjectWork is the definition's Work of the Subject, as creation reads it.
func subjectWork(t testing.TB, works domain.AgentWorkRepository, agentID string, subject domain.WorkSubject) domain.WorkCreationFacts {
	t.Helper()
	work, err := works.FindBySubject(t.Context(), agentID, subject)
	require.NoError(t, err)
	return domain.WorkCreationFacts{SubjectWork: work}
}

// requireStartRequested asserts that the Work holds the request to begin its
// admitted start's execution, which the Policies and the Work's recovery
// pass on.
func requireStartRequested(t testing.TB, works domain.AgentWorkRepository, workID string) domain.LaunchSpecification {
	t.Helper()
	work, err := works.Get(t.Context(), workID)
	require.NoError(t, err)
	for _, request := range work.ExecutionRequests() {
		if _, ok := request.(domain.WorkStartAdmitted); ok {
			return work.LaunchSpecification()
		}
	}
	require.Fail(t, "the Work holds no start request")
	return domain.LaunchSpecification{}
}

// executionsOf is the AgentExecution repository over db.
func executionsOf(db *xorm.Engine) *execution.AgentExecutionRepository {
	return execution.NewAgentExecutionRepository(db, time.Now)
}

// latest is the Work's most recently begun execution after the Policies ran,
// nil when it has none.
func (r *runs) latest(ctx context.Context, workID string) *domain.AgentExecution {
	r.t.Helper()
	r.coordinate(ctx, workID)
	run, err := r.executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return nil
	}
	require.NoError(r.t, err)
	return run
}

// launchOf is the LaunchSpecification the execution took from its Work,
// without the Computer name the Policies add and the preparation bound its
// seating fixes.
func launchOf(run *domain.AgentExecution) domain.LaunchSpecification {
	launch := run.Launch
	// What beginning the execution fills in is not the Work's own.
	launch.ComputerName, launch.Purpose, launch.PreparationBound = "", "", time.Time{}
	return launch
}

// recordWorkCheckouts records checkout outcomes on the Work as its execution
// reports them when its checkout ends.
func recordWorkCheckouts(t *testing.T, works domain.AgentWorkRepository, id string, checkouts ...domain.RepositoryCheckout) {
	t.Helper()
	ctx := context.Background()
	work, err := works.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: id, Checkouts: checkouts}))
	require.NoError(t, works.Save(ctx, work))
}
