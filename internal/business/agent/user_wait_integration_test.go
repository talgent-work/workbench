//go:build integration

package agent_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

func TestModuleRestartReclaimsEndedTurnWithoutTimeoutOptIn(t *testing.T) {
	testModuleProcessRecovery(t, false)
}

func TestModuleRestartRecoversReadinessWhenBothNotificationsAreLost(t *testing.T) {
	testModuleProcessRecovery(t, true)
}

func testModuleProcessRecovery(t *testing.T, loseReadiness bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dsn := mysqltest.Database(t)
	seed, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = seed.Close() })
	port := &sessionCommandRecorder{}
	port.observe(command.WorkProcessObservation{Running: true})
	runs := &moduleRuns{}
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	start := func(recover bool) (*application.Application, agentprivatev1connect.AgentServiceClient, func()) {
		t.Helper()
		db, err := xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		mux := http.NewServeMux()
		var registry *application.Application
		options := []fx.Option{agent.Module, moduleCatalog(), sessionStateTransport(t), fx.NopLogger, fx.Populate(&registry), storageStub(),
			fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
			fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
			fx.Provide(func() kubernetes.Interface { return fake.NewClientset(quota.DeepCopy()) }),
			fx.Provide(func() command.DispatcherPort { return port }),
			runs.option(),
		}
		if recover {
			options = append(options, agent.RecoveryModule, recoveryQueues(t), fx.Provide(func() command.ExecutionEnvironmentPort { return realizedEnvironments{} }), fx.Supply(domain.LeaseTerms{}, commandtest.CreationWaits))
		}
		runtime := fx.New(options...)
		require.NoError(t, runtime.Err())
		require.NoError(t, runtime.Start(ctx))
		server := httptest.NewServer(mux)
		var once sync.Once
		stop := func() {
			once.Do(func() {
				server.Close()
				shutdown, release := context.WithTimeout(context.Background(), 5*time.Second)
				defer release()
				require.NoError(t, runtime.Stop(shutdown))
				require.NoError(t, db.Close())
			})
		}
		t.Cleanup(stop)
		return registry, agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))), stop
	}
	registry, _, stop := start(false)
	definitionID, err := registry.CreateAgentDefinition(ctx, application.CreateAgentDefinition{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}})
	require.NoError(t, err)
	id, err := registry.Commands.CreateWork.Handle(ctx, command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), SubjectID: "owner", AgentID: definitionID})
	require.NoError(t, err)
	ref := id
	runs.realize(t, ctx, id)
	if loseReadiness {
		stop()
		port.observe(command.WorkProcessObservation{Running: true, Ready: true})
		_, client, shutdown := start(true)
		defer shutdown()
		require.Eventually(t, func() bool {
			response, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: id})
			return err == nil && response.State == agentprivatev1.WorkState_WORK_STATE_WORKING
		}, 5*time.Second, 20*time.Millisecond, "current native readiness must recover both lost startup notifications")
		return
	}
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, registry.Commands.ApplyExecutionFeedback.Handle(ctx, command.ExecutionFeedback{Kind: kind, WorkID: ref, ProcessID: launchedProcess}))
	}
	// Supply a deterministic accepted idle origin, then destroy every module
	// and connection before the production loop is allowed to advance it.
	observer, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	origin := time.Now().Add(-31 * time.Minute).UTC().Truncate(time.Millisecond)
	works := infrastructure.NewAgentWorkRepository(observer, time.Now)
	executions := execution.NewAgentExecutionRepository(observer, time.Now)
	feedback := command.NewApplyExecutionFeedbackHandler(func() time.Time { return origin }, works, executions, command.NewExecutionCoordination(time.Now, works, executions, &computertest.Admission{}, port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	// A Turn ending at origin leaves the Work idle since then.
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ObservationSequence: 1, Kind: command.TurnStarted, WorkID: ref, ProcessID: "process"}))
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ObservationSequence: 2, Kind: command.TurnFinished, WorkID: ref, ProcessID: "process", TurnCompleted: false}))
	require.NoError(t, observer.Close())
	stop()
	recovered, client, stop := start(true)
	var observed *agentprivatev1.GetWorkResponse
	require.Eventually(t, func() bool {
		response, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: id})
		if err != nil {
			return false
		}
		observed = response
		return observed.State == agentprivatev1.WorkState_WORK_STATE_STOPPING
	}, 5*time.Second, 20*time.Millisecond, "Agent recovery must admit the stored timeout without Work or a new caller")
	require.False(t, observed.Waiting, "stopping ends the Waiting interval")
	require.Equal(t, "waiting_timeout", observed.StopReason)
	require.NotEmpty(t, observed.StopRequestId)
	runs.awaitExitRequest(t, ctx, ref, observed.StopRequestId)
	require.Eventually(t, func() bool { port.mu.Lock(); defer port.mu.Unlock(); return len(port.stops) > 0 }, 5*time.Second, 10*time.Millisecond, "the execution's reconciliation signals the exit")
	port.assertSignaled(t, ref, observed.StopRequestId)
	require.Len(t, launches(t, seed), 1, "recovery must not begin a new execution")
	// The exit fact is redelivered until applied; the restarted
	// module settles the retained Work from it.
	require.NoError(t, recovered.Commands.ApplyExecutionFeedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: ref, ProcessID: launchedProcess, ExitCode: 143}))
	runs.awaitRetired(t, ctx, ref)
	require.Eventually(t, func() bool {
		response, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: id})
		return err == nil && response.State == agentprivatev1.WorkState_WORK_STATE_STOPPED
	}, 5*time.Second, 20*time.Millisecond, "the retired execution's reconciliation confirms its release and settles the Work")
	stop()
}
