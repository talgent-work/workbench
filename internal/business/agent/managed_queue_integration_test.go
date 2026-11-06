//go:build integration

package agent_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"

	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/computer"
	"github.com/talgent-work/workbench/pkg/mysqltest"
)

// managedLaptop is employee-a's Managed Computer: one Desk, queuing runs
// while full.
const managedLaptop = "0195f3c1-0000-7000-8000-00000000aaaa"

type managedQueue struct {
	oneRoomCloud
	port     *sessionCommandRecorder
	recovery *command.RecoverWorkHandler
	seating  *command.SeatWaitingStartHandler
}

func startManagedQueue(t *testing.T, ctx context.Context) *managedQueue {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UnixMilli()
	_, err = db.Exec(`INSERT INTO computer (id, kind, owner_id, name, capacity, occupied, admission_policy, waiting_line, reachable, unreachable_since, talgent_computer_version, claude_code_version, credential_digest, credential_expires_at, state, version, created_at, updated_at, deleted_at)
 VALUES (?, 'managed', 'employee-a', 'Laptop', '{"desks":1}', '{}', 'queue', '[]', 1, 0, '1.0.0', '', ?, ?, 'active', 1, ?, ?, 0)`,
		managedLaptop, "0000000000000000000000000000000000000000000000000000000000000000", now+time.Hour.Milliseconds(), now, now)
	require.NoError(t, err)
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	kube := fake.NewClientset(quota)
	port := &sessionCommandRecorder{}
	processes := &lifecycleProcesses{}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	var recovery *command.RecoverWorkHandler
	var seating *command.SeatWaitingStartHandler
	app := fx.New(computer.Module, agent.Module, moduleCatalog(), sessionStateTransport(t), fx.NopLogger, storageStub(), testRedis(t),
		fx.Decorate(func(application.PendingInteractionsPort) application.PendingInteractionsPort { return port }),
		fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, agent.ComputerConfig{Address: server.URL, Token: "computer"}, testCloud("4", "4Gi", "64")),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(func() kubernetes.Interface { return kube }),
		fx.Provide(func() domain.ExecutionPort { return port }),
		fx.Provide(func() command.InputBufferPort { return port }),
		fx.Decorate(func(command.ProcessObservationPort) command.ProcessObservationPort { return processes }),
		fx.Invoke(func(handler *command.RecoverWorkHandler, seats *command.SeatWaitingStartHandler) {
			recovery, seating = handler, seats
		}),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() { require.NoError(t, app.Stop(context.Background())) })
	return &managedQueue{
		oneRoomCloud: oneRoomCloud{client: agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL), feedback: agentprivatev1connect.NewExecutionFeedbackServiceClient(server.Client(), server.URL), processes: processes, db: db, quota: quota, kube: kube},
		port:         port, recovery: recovery, seating: seating,
	}
}

func (q *managedQueue) createOn(ctx context.Context, definition, computerID, subject string) (string, error) {
	response, err := q.client.CreateWork(ctx, sessionControlRequest(&agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: "employee-a", AgentId: definition, WorkspaceReference: subject, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: subject}, ProjectId: "project-a", ComputerId: computerID}))
	if err != nil {
		return "", err
	}
	return response.Msg.WorkId, nil
}

func (q *managedQueue) state(t *testing.T, ctx context.Context, workID string) agentprivatev1.WorkState {
	t.Helper()
	response, err := q.client.GetWork(ctx, sessionControlRequest(&agentprivatev1.GetWorkRequest{SubjectId: "employee-a", WorkId: workID}))
	require.NoError(t, err)
	return response.Msg.GetState()
}

func (q *managedQueue) startedFor(workID string) int {
	q.port.mu.Lock()
	defer q.port.mu.Unlock()
	count := 0
	for _, start := range q.port.starts {
		if start.WorkID == workID {
			count++
		}
	}
	return count
}

func (q *managedQueue) deskOf(t *testing.T, workID string) (string, string) {
	t.Helper()
	var desk struct {
		ID    string `xorm:"id"`
		State string `xorm:"state"`
	}
	found, err := q.db.SQL("SELECT id, state FROM computer_desk WHERE work_id = ? ORDER BY opened_at DESC, id DESC LIMIT 1", workID).Get(&desk)
	require.NoError(t, err)
	require.True(t, found)
	return desk.ID, desk.State
}

// The run ends and its compute is released: its Desk leaves.
func (q *managedQueue) fail(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	q.report(t, ctx, workID, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, 1)
	q.processes.observe(domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true})
	q.report(t, ctx, workID, agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED, 0)
	require.Eventually(t, func() bool { _, state := q.deskOf(t, workID); return state == "vacated" }, 10*time.Second, 50*time.Millisecond)
}

// A full queueing Managed Computer keeps a run's Work Starting while its Desk
// waits and starts it once seated; a stop or close withdraws a queued start;
// a full rejecting one rejects with its reason; the Cloud admits as before.
func TestAFullManagedComputerQueuesOrRejectsByItsPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	q := startManagedQueue(t, ctx)
	definition := q.definition(t, ctx)

	running, err := q.createOn(ctx, definition, managedLaptop, "work-a")
	require.NoError(t, err)
	require.Equal(t, 1, q.startedFor(running))
	queued, err := q.createOn(ctx, definition, managedLaptop, "work-b")
	require.NoError(t, err, "a full queueing Computer admits the run into its line")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, q.state(t, ctx, queued))
	require.Zero(t, q.startedFor(queued), "a queued start asks for no startup")
	_, desk := q.deskOf(t, queued)
	require.Equal(t, "waiting", desk)
	require.NoError(t, q.recovery.Handle(ctx, queued))
	require.Zero(t, q.startedFor(queued), "recovery leaves a start waiting while its Desk waits")

	// A stop withdraws a queued first start: the Work is closed at once.
	withdrawn, err := q.createOn(ctx, definition, managedLaptop, "work-c")
	require.NoError(t, err)
	_, err = q.client.StopWork(ctx, sessionControlRequest(&agentprivatev1.StopWorkRequest{SubjectId: "employee-a", WorkId: withdrawn, RequestId: uuid.NewString()}))
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, q.state(t, ctx, withdrawn))
	require.Eventually(t, func() bool { _, state := q.deskOf(t, withdrawn); return state == "vacated" }, 10*time.Second, 50*time.Millisecond)

	// The running Work ends; its room seats the queued start, which starts
	// with its Startup Deadline counted from seating.
	q.fail(t, ctx, running)
	queuedDesk, desk := q.deskOf(t, queued)
	require.Equal(t, "occupied", desk)
	require.ErrorIs(t, q.seating.DeskAssigned(ctx, queued, uuid.NewString(), time.Now()), command.ErrDeskNotHeld, "a Desk the Work does not hold is asked for again")
	require.ErrorIs(t, q.seating.DeskAssigned(ctx, uuid.NewString(), queuedDesk, time.Now()), command.ErrDeskNotHeld, "so is one whose Work is not visible yet")
	time.Sleep(10 * time.Millisecond) // Agent's clock moves past Computer's seating.
	require.NoError(t, q.recovery.Handle(ctx, queued), "recovery finds the seated Desk whose Desk Assigned it missed")
	require.Equal(t, 1, q.startedFor(queued))
	var process struct {
		DeskWaiting bool       `json:"desk_waiting"`
		SeatedAt    *time.Time `json:"seated_at"`
	}
	var raw string
	_, err = q.db.SQL("SELECT harness_process FROM agent_session WHERE id = ?", queued).Get(&raw)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(raw), &process))
	require.False(t, process.DeskWaiting)
	var since int64
	_, err = q.db.SQL("SELECT since FROM computer_desk WHERE id = ?", queuedDesk).Get(&since)
	require.NoError(t, err)
	require.NotNil(t, process.SeatedAt)
	require.Equal(t, since, process.SeatedAt.UnixMilli(), "the Startup Deadline counts from Computer's seating")
	require.NoError(t, q.seating.DeskAssigned(ctx, queued, queuedDesk, time.Now()), "a repeated fact changes nothing")
	require.Equal(t, 1, q.startedFor(queued))

	// A Restart queued behind it is withdrawn by a stop back to Failed, and
	// the Work is then closed.
	_, err = q.client.RestartWork(ctx, sessionControlRequest(&agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: running, RequestId: uuid.NewString()}))
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, q.state(t, ctx, running))
	require.Equal(t, 1, q.startedFor(running), "the queued Restart asks for no startup")
	_, err = q.client.StopWork(ctx, sessionControlRequest(&agentprivatev1.StopWorkRequest{SubjectId: "employee-a", WorkId: running, RequestId: uuid.NewString()}))
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_FAILED, q.state(t, ctx, running))
	_, err = q.client.CloseWork(ctx, sessionControlRequest(&agentprivatev1.CloseWorkRequest{SubjectId: "employee-a", WorkId: running, RequestId: uuid.NewString()}))
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, q.state(t, ctx, running))

	// A full rejecting Managed Computer rejects with its reason.
	_, err = q.db.Exec("UPDATE computer SET admission_policy = 'reject', version = version + 1 WHERE id = ?", managedLaptop)
	require.NoError(t, err)
	_, err = q.createOn(ctx, definition, managedLaptop, "work-d")
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.ErrorContains(t, err, "as many Works as its Capacity allows")

	// The Cloud admits as before Clouds were Computers.
	cloudWork, err := q.createOn(ctx, definition, "", "work-e")
	require.NoError(t, err)
	require.Equal(t, 1, q.startedFor(cloudWork))
}
