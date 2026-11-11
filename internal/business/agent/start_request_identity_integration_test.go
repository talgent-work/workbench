//go:build integration

package agent_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// creation is a CreateWork request of the employee's Work about the subject.
func creation(definition, requestID, subject string) *agentprivatev1.CreateWorkRequest {
	return &agentprivatev1.CreateWorkRequest{RequestId: requestID, SubjectId: "employee-a", AgentId: definition, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: subject}, ProjectId: "project-a"}
}

// desksOf counts the Desks the Work's runs ever took on its Computer.
func (c *oneRoomCloud) desksOf(t *testing.T, workID string) int64 {
	t.Helper()
	count, err := c.db.Table("computer_desk").Where("work_id = ?", workID).Count()
	require.NoError(t, err)
	return count
}

// startsOf counts the Work's starts that reached execution.
func (c *oneRoomCloud) startsOf(t *testing.T, workID string) int64 {
	t.Helper()
	count, err := c.db.Table("agent_execution").Where("session_id = ?", workID).Count()
	require.NoError(t, err)
	return count
}

func (c *oneRoomCloud) workState(t *testing.T, ctx context.Context, workID string) agentprivatev1.WorkState {
	t.Helper()
	response, err := c.client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "employee-a", WorkId: workID})
	require.NoError(t, err)
	return response.GetState()
}

// awaitState waits until the Work applied its execution's reports.
func (c *oneRoomCloud) awaitState(t *testing.T, ctx context.Context, workID string, want agentprivatev1.WorkState) {
	t.Helper()
	require.Eventually(t, func() bool { return c.workState(t, ctx, workID) == want }, 10*time.Second, 50*time.Millisecond, "the Work reaches %s", want)
}

// awaitReleased waits until the Work's current start released its compute,
// its Desk left and the Work applied that release. The Work learns of the
// release from its execution's ExecutionReleased report, after the execution
// recorded it; until then its current start still holds the Desk, so a
// repeated start answers that admission and a new one is refused while the
// reclamation is pending.
func (c *oneRoomCloud) awaitReleased(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	c.runs.await(t, ctx, workID, func(e *domain.AgentExecution) bool { return e.State == domain.ExecutionReleased }, "the run's compute is released")
	require.Eventually(t, func() bool { return c.desks(t, "occupied") == 0 }, 10*time.Second, 50*time.Millisecond, "the run's Desk leaves")
	works := infrastructure.NewAgentWorkRepository(c.db, time.Now)
	require.Eventually(t, func() bool {
		work, err := works.Get(ctx, workID)
		require.NoError(t, err)
		return work.HarnessProcess != nil && work.HarnessProcess.ComputeReclaimed
	}, 10*time.Second, 50*time.Millisecond, "the Work applies the release")
}

// fail ends the Work's current run before it became ready.
func (c *oneRoomCloud) fail(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	c.runs.awaitLaunched(t, ctx, workID)
	c.report(t, ctx, workID, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, 1)
	c.awaitReleased(t, ctx, workID)
	c.awaitState(t, ctx, workID, agentprivatev1.WorkState_WORK_STATE_FAILED)
}

// stop runs the Work's current start to readiness and stops it.
func (c *oneRoomCloud) stop(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	c.runs.awaitLaunched(t, ctx, workID)
	c.runs.ready(t, ctx, workID)
	stopID := uuid.NewString()
	_, err := c.client.StopWork(sessionControlRequest(ctx), &agentprivatev1.StopWorkRequest{SubjectId: "employee-a", WorkId: workID, RequestId: stopID})
	require.NoError(t, err)
	c.runs.awaitExitRequest(t, ctx, workID, stopID)
	c.report(t, ctx, workID, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, 143)
	c.awaitReleased(t, ctx, workID)
	c.awaitState(t, ctx, workID, agentprivatev1.WorkState_WORK_STATE_STOPPED)
}

// A repeated creation reaches the Work and the Desk its request identity
// derives; another request for the same Subject returns that Work without a
// Desk of its own.
func TestARepeatedCreationReachesTheSameWorkAndDesk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cloud := startOneRoomCloud(t, ctx)
	definition := cloud.definition(t, ctx)
	request := creation(definition, uuid.NewString(), "work-a")
	first, err := cloud.client.CreateWork(sessionControlRequest(ctx), request)
	require.NoError(t, err)
	repeated, err := cloud.client.CreateWork(sessionControlRequest(ctx), request)
	require.NoError(t, err)
	require.Equal(t, first.GetWorkId(), repeated.GetWorkId(), "a repeat returns the Work its identity created")
	require.EqualValues(t, 1, cloud.desksOf(t, first.GetWorkId()), "and asks for no other Desk")

	reuse, err := cloud.client.CreateWork(sessionControlRequest(ctx), creation(definition, uuid.NewString(), "work-a"))
	require.NoError(t, err)
	require.Equal(t, first.GetWorkId(), reuse.GetWorkId(), "a creation with the Subject returns the Work that holds it")
	require.EqualValues(t, 1, cloud.desksOf(t, first.GetWorkId()))
	cloud.runs.awaitLaunched(t, ctx, first.GetWorkId())
	require.EqualValues(t, 1, cloud.startsOf(t, first.GetWorkId()), "one start reaches execution")
}

// A repeated Restart or Resume returns the admission its Desk holds while
// that start runs; once the start ended and its Desk left, a late repeat
// starts nothing.
func TestARepeatedStartAnswersItsAdmissionAndALateRepeatStartsNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cloud := startOneRoomCloud(t, ctx)
	definition := cloud.definition(t, ctx)
	id, err := cloud.create(ctx, definition, "work-a")
	require.NoError(t, err)
	cloud.fail(t, ctx, id)

	restart := &agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: id, RequestId: uuid.NewString()}
	for range 2 {
		_, err = cloud.client.RestartWork(sessionControlRequest(ctx), restart)
		require.NoError(t, err, "a repeat returns the same admission")
	}
	cloud.runs.awaitLaunched(t, ctx, id)
	require.EqualValues(t, 2, cloud.startsOf(t, id), "one Restart reaches execution")
	require.EqualValues(t, 2, cloud.desksOf(t, id))
	cloud.fail(t, ctx, id)
	_, err = cloud.client.RestartWork(sessionControlRequest(ctx), restart)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a late repeat whose Desk left starts nothing")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_FAILED, cloud.workState(t, ctx, id))
	require.EqualValues(t, 2, cloud.startsOf(t, id))

	_, err = cloud.client.RestartWork(sessionControlRequest(ctx), &agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: id, RequestId: uuid.NewString()})
	require.NoError(t, err, "a new request starts the Work again")
	cloud.stop(t, ctx, id)
	resume := &agentprivatev1.ResumeWorkRequest{SubjectId: "employee-a", WorkId: id, RequestId: uuid.NewString()}
	for range 2 {
		_, err = cloud.client.ResumeWork(sessionControlRequest(ctx), resume)
		require.NoError(t, err, "a repeat returns the same admission")
	}
	cloud.runs.awaitLaunched(t, ctx, id)
	require.EqualValues(t, 4, cloud.startsOf(t, id), "one Resume reaches execution")
	cloud.stop(t, ctx, id)
	_, err = cloud.client.ResumeWork(sessionControlRequest(ctx), resume)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a late repeat whose Desk left starts nothing")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, cloud.workState(t, ctx, id))
	require.EqualValues(t, 4, cloud.startsOf(t, id))
}

// One start request identity names one Work: Computer refuses its Desk for
// another Work, which stays as it was.
func TestAStartRequestIdentityNamesOneWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cloud := startOneRoomCloud(t, ctx)
	definition := cloud.definition(t, ctx)
	first, err := cloud.create(ctx, definition, "work-a")
	require.NoError(t, err)
	cloud.fail(t, ctx, first)
	second, err := cloud.create(ctx, definition, "work-b")
	require.NoError(t, err)
	cloud.fail(t, ctx, second)

	requestID := uuid.NewString()
	_, err = cloud.client.RestartWork(sessionControlRequest(ctx), &agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: first, RequestId: requestID})
	require.NoError(t, err)
	_, err = cloud.client.RestartWork(sessionControlRequest(ctx), &agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: second, RequestId: requestID})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_FAILED, cloud.workState(t, ctx, second))
	require.EqualValues(t, 1, cloud.startsOf(t, second))
}

// The private contract no longer serves the start-request RPCs it removed:
// the service answers them as unimplemented procedures.
func TestTheRemovedStartRequestRPCsAreNotServed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cloud := startOneRoomCloud(t, ctx)
	for _, procedure := range []string{"GetWorkStartup", "GetWorkCreation", "CancelWorkCreation", "GetStartAdmission", "CancelWorkRegistration"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, cloud.server+"/agent.private.v1.AgentService/"+procedure, strings.NewReader("{}"))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer control")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		_ = response.Body.Close()
		require.Equal(t, http.StatusNotImplemented, response.StatusCode, procedure)
	}
}

// The Resume a Managed Work is owed when its machine reconnects is requested
// under one deterministic request identity: replicas applying the same
// reconnection at once start it once.
func TestTheOwedResumeOnReconnectionStartsOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	q := startManagedQueue(t, ctx)
	definition := q.definition(t, ctx)
	id, err := q.createOn(ctx, definition, managedLaptop, "work-a")
	require.NoError(t, err)
	q.stop(t, ctx, id)

	setReachable := func(reachable bool) {
		t.Helper()
		since := int64(0)
		if !reachable {
			since = time.Now().UnixMilli()
		}
		_, err := q.db.Exec("UPDATE computer SET reachable = ?, unreachable_since = ?, version = version + 1 WHERE id = ?", reachable, since, managedLaptop)
		require.NoError(t, err)
	}
	setReachable(false)
	_, err = q.client.ResumeWork(sessionControlRequest(ctx), &agentprivatev1.ResumeWorkRequest{SubjectId: "employee-a", WorkId: id, RequestId: uuid.NewString()})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "an unreachable machine refuses the Resume")
	var due bool
	_, err = q.db.SQL("SELECT reach_resume_due FROM agent_session WHERE id = ?", id).Get(&due)
	require.NoError(t, err)
	require.True(t, due, "the refused Resume is owed on reconnection")
	_, err = q.buffer.BufferInput(ctx, id, command.ExecutionInput{ID: uuid.NewString(), Text: "read me when back"})
	require.NoError(t, err)

	setReachable(true)
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- q.reconnection.Reconnected(ctx, managedLaptop) }()
	}
	for range 2 {
		require.NoError(t, <-results)
	}
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, q.state(t, ctx, id))
	q.runs.awaitLaunched(t, ctx, id)
	require.EqualValues(t, 2, q.startsOf(t, id), "the owed Resume starts once")
	require.EqualValues(t, 2, q.desksOf(t, id), "on one Desk")
}
