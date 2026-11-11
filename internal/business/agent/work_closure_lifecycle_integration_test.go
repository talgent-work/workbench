//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// closeWork asks to close the Work as its owner under requestID.
func (m *lifecycleModule) closeWork(workID, requestID string) (*agentprivatev1.CloseWorkResponse, error) {
	return m.client.CloseWork(m.control(), &agentprivatev1.CloseWorkRequest{SubjectId: workOwner, WorkId: workID, RequestId: requestID})
}

// requireNoNextStart requires that the Work admits neither a Resume nor a
// Restart.
func (m *lifecycleModule) requireNoNextStart(workID string) {
	m.t.Helper()
	require.Equal(m.t, connect.CodeFailedPrecondition, connect.CodeOf(m.resume(workOwner, workID)))
	require.Equal(m.t, connect.CodeFailedPrecondition, connect.CodeOf(m.restartWork(workOwner, workID)))
}

// Admitted input fences a close until the Harness settles it and its Turn
// ends; the close is then accepted and stops the Work first.
func TestCloseIsFencedByAdmittedInput(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking, turns: 1}).ID
	m.start()
	input := uuid.NewString()
	require.NoError(t, m.deliver(work, input, "finish before close"))
	_, err := m.closeWork(work, "close")
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "production module must fence close against admitted input")
	m.port.settle(input)
	require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_INPUT_SETTLED, InputId: input, InputOutcome: agentprivatev1.InputOutcome_INPUT_OUTCOME_COMPLETED}))
	require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 20, TurnCompleted: true}))
	accepted, err := m.closeWork(work, "close")
	require.NoError(t, err)
	require.Equal(t, work, accepted.WorkId)
	require.Equal(t, "close", accepted.RequestId)
	m.runs.awaitExitRequest(t, m.ctx, work, "close")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, m.state(work))
}

// A running Work is stopped before it closes: while the close is kept it
// admits no next start, its recovery leaves it Stopping, and once its process
// exited and its compute was released it is Stopped, the close still kept.
func TestCloseStopsARunningWorkFirst(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking, turns: 1}).ID
	m.start()
	_, err := m.closeWork(work, "close")
	require.NoError(t, err)
	m.runs.awaitExitRequest(t, m.ctx, work, "close")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, m.state(work), "a running Work is stopped before it closes")
	require.NoError(t, m.recovery.Handle(m.ctx, work))
	m.requireNoNextStart(work)
	require.NoError(t, m.reportProcess(work, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, launchedProcess, 0))
	m.runs.awaitRetired(t, m.ctx, work)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, m.state(work))
	m.runs.release(t, m.ctx, work)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, m.state(work), "the close applies after the stop, not with it")
}

// Once stopped, the Work's recovery, which its execution-ended event wakes
// and the sweep finds, applies the kept close.
func TestRecoveryAppliesAKeptClose(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkStopped, closeRequest: "close"}).ID
	m.start()
	require.NoError(t, application.NewRecovery(m.app, nil, nil, nil, nil, nil, nil).RecoverWork(m.ctx, work))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, m.state(work))
}

// A Closed Work admits neither a Resume nor a Restart, so closure begins no
// execution.
func TestClosedWorkBeginsNoExecution(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkClosed, closeRequest: "close"}).ID
	m.start()
	m.requireNoNextStart(work)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, m.state(work))
	require.Len(t, launches(t, m.db), 1, "closure must not begin any new execution")
}
