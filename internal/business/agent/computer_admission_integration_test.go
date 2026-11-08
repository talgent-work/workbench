//go:build integration

package agent_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/computer"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// A Cloud with room for one run, admitting through the real Agent and
// Computer modules and MySQL; each execution's reconciliation runs on the
// recovery queues, its environment realized at once.
type oneRoomCloud struct {
	client   agentprivatev1connect.AgentServiceClient
	feedback agentprivatev1connect.ExecutionFeedbackServiceClient
	runs     *moduleRuns
	// deleting keeps a retired execution's environment until it is cleared.
	deleting *atomic.Bool
	// recovery wakes an execution as the sandbox observer does when its
	// environment's deletion completes.
	recovery *application.Recovery
	db       *xorm.Engine
	quota    *corev1.ResourceQuota
	kube     *fake.Clientset
}

func startOneRoomCloud(t *testing.T, ctx context.Context) *oneRoomCloud {
	t.Helper()
	deleting := &atomic.Bool{}
	c := startOneRoomCloudWith(t, ctx, realizedEnvironments{pendingDeletion: deleting})
	c.deleting = deleting
	return c
}

// startOneRoomCloudWith starts the one-room Cloud whose execution environments
// environments realizes.
func startOneRoomCloudWith(t *testing.T, ctx context.Context, environments command.ExecutionEnvironmentPort) *oneRoomCloud {
	t.Helper()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	kube := fake.NewClientset(quota.DeepCopy())
	port := &sessionCommandRecorder{}
	runs := &moduleRuns{}
	var recovery *application.Recovery
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	app := fx.New(computer.Module, agent.Module, moduleCatalog(), sessionStateTransport(t), fx.NopLogger, storageStub(), testRedis(t),
		agent.RecoveryModule, recoveryQueues(t), fx.Provide(func() command.ExecutionEnvironmentPort { return environments }), fx.Supply(domain.LeaseTerms{}), runs.option(),
		fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, agent.ComputerConfig{Address: server.URL, Token: "computer"}, testCloud("1", "4Gi", "64")),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(func() kubernetes.Interface { return kube }),
		fx.Provide(func() command.DispatcherPort { return port }),
		fx.Populate(&recovery),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() { require.NoError(t, app.Stop(context.Background())) })
	return &oneRoomCloud{recovery: recovery,
		client:   agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))),
		feedback: agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))),
		runs:     runs, db: db, quota: quota, kube: kube,
	}
}

func (c *oneRoomCloud) definition(t *testing.T, ctx context.Context) string {
	t.Helper()
	created, err := c.client.CreateAgentDefinition(sessionControlRequest(ctx), &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"employee-a"}, AgentName: "Fixture Agent",
		UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "employee-a"}, Purpose: "work",
		HomeBinding:  &agentprivatev1.HomeBinding{OwnerId: "employee-a", Namespace: "personal"},
		Capabilities: &agentprivatev1.Capabilities{Prompt: "base prompt", StartupAgent: "custom:base", RequiredPlugins: []string{"custom"}},
	})
	require.NoError(t, err)
	return created.AgentId
}

func (c *oneRoomCloud) create(ctx context.Context, definition, subject string) (string, error) {
	response, err := c.client.CreateWork(sessionControlRequest(ctx), &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: "employee-a", AgentId: definition, WorkspaceReference: subject, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: subject}, ProjectId: "project-a"})
	if err != nil {
		return "", err
	}
	return response.WorkId, nil
}

func (c *oneRoomCloud) report(t *testing.T, ctx context.Context, workID string, kind agentprivatev1.FeedbackKind, exitCode int32) {
	t.Helper()
	req := &agentprivatev1.ReportRequest{WorkId: workID, Kind: kind, ProcessId: launchedProcess, ExitCode: exitCode}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer feedback")
	_, err := c.feedback.Report(ctx, req)
	require.NoError(t, err)
}

func (c *oneRoomCloud) desks(t *testing.T, state string) int64 {
	t.Helper()
	count, err := c.db.Table("computer_desk").Where("state = ?", state).Count()
	require.NoError(t, err)
	return count
}

// Every run takes a Desk before it starts, so two concurrent starts never both
// claim the last room; the Desk leaves once its process ended and its
// compute is released, and the room seats the next start.
func TestCloudSeatsOneRunPerRoomAndFreesItAfterRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cloud := startOneRoomCloud(t, ctx)
	definition := cloud.definition(t, ctx)

	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	barrier := make(chan struct{})
	for _, subject := range []string{"work-a", "work-b"} {
		go func() {
			<-barrier
			id, err := cloud.create(ctx, definition, subject)
			results <- result{id: id, err: err}
		}()
	}
	close(barrier)
	var seated string
	rejected := 0
	for range 2 {
		r := <-results
		if r.err != nil {
			require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(r.err), "a full Cloud rejects as before Clouds were Computers")
			require.ErrorContains(t, r.err, "capacity")
			rejected++
			continue
		}
		seated = r.id
	}
	require.Equal(t, 1, rejected, "only one start claims the last room")
	require.NotEmpty(t, seated)
	require.EqualValues(t, 1, cloud.desks(t, "occupied"))

	// The run fails and its compute is released: its Desk leaves.
	cloud.runs.awaitLaunched(t, ctx, seated)
	require.EqualValues(t, 1, cloud.desks(t, "occupied"), "a running run holds its room")
	cloud.deleting.Store(true)
	cloud.report(t, ctx, seated, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, 1)
	retired := cloud.runs.await(t, ctx, seated, (*domain.AgentExecution).Retired, "the Work retires its execution")
	require.Never(t, func() bool { return cloud.desks(t, "occupied") == 0 }, 250*time.Millisecond, 25*time.Millisecond, "an exited run holds its room until compute is released")
	cloud.deleting.Store(false)
	require.NoError(t, cloud.recovery.WakeExecution(ctx, retired.ID), "the deletion's completion wakes the execution")
	require.Eventually(t, func() bool { return cloud.desks(t, "occupied") == 0 }, 10*time.Second, 50*time.Millisecond)
	require.EqualValues(t, 1, cloud.desks(t, "vacated"))
	cloud.runs.await(t, ctx, seated, func(e *domain.AgentExecution) bool { return e.State == domain.ExecutionReleased }, "the failed run's compute is released")

	second, err := cloud.create(ctx, definition, "work-c")
	require.NoError(t, err, "the freed room seats the next start")
	require.NotEqual(t, seated, second)
}

// A run the cluster's quota cannot fit is rejected with today's answer, and
// holds no Desk.
func TestCloudRejectsARunItsClusterQuotaCannotFit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cloud := startOneRoomCloud(t, ctx)
	definition := cloud.definition(t, ctx)
	full := cloud.quota.DeepCopy()
	full.Status.Used = full.Status.Hard.DeepCopy()
	_, err := cloud.kube.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(ctx, full, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = cloud.create(ctx, definition, "work-a")
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.EqualValues(t, 0, cloud.desks(t, "occupied"))
	require.EqualValues(t, 0, cloud.desks(t, "waiting"))

	var computerID string
	found, err := cloud.db.Table("computer").Cols("id").Where("kind = 'cloud'").Get(&computerID)
	require.NoError(t, err)
	require.True(t, found)
	_, err = cloud.kube.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(ctx, cloud.quota.DeepCopy(), metav1.UpdateOptions{})
	require.NoError(t, err)
	id, err := cloud.create(ctx, definition, "work-a")
	require.NoError(t, err)
	var workComputer string
	_, err = cloud.db.Table("agent_session").Cols("computer_id").Where("id = ?", id).Get(&workComputer)
	require.NoError(t, err)
	require.Equal(t, computerID, workComputer, "every Work carries its Computer")
}
