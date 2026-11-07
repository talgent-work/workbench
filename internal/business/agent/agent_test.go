package agent_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-jimu/components/ddd/message"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	"github.com/talgent-work/workbench/internal/pkg/nativeauth"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

// Exercise the production module graph and registered HTTP boundary. Storage
// and execution are deliberately unused: unauthenticated grants must be rejected
// before either dependency can be called.
func TestModuleMountsAuthenticatedSessionAPI(t *testing.T) {
	mux := http.NewServeMux()
	app := fx.New(agent.Module, sessionStateTransport(t), fx.NopLogger, storageStub(),
		fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return nil }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(func() kubernetes.Interface { return fake.NewClientset() }),
		fx.Provide(func() command.RunningHarnessPort { return unusedExecutionPort{} }),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(context.Background()))
	defer app.Stop(context.Background())
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	_, err := client.CreateWork(context.Background(), &agentprivatev1.CreateWorkRequest{SubjectId: "untrusted", AgentId: "agent", WorkspaceReference: "other-workspace"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.GetAgentDefinitionForPurpose(context.Background(), &agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "other"}, Purpose: "work"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.ResolveEmployeeAgents(context.Background(), &agentprivatev1.ResolveEmployeeAgentsRequest{EmployeeIds: []string{"other"}, Purpose: "work"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.ResolveWorkAgents(context.Background(), &agentprivatev1.ResolveWorkAgentsRequest{WorkIds: []string{"other"}})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	public := agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	_, err = public.ChangeWorkAgentAppearance(context.Background(), &agentpublicv1.ChangeWorkAgentAppearanceRequest{AgentId: "agent", Name: "Imposter", Icon: &agentpublicv1.AgentIcon{Glyph: "bot", Color: "green"}})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.GetMyWorkAgent(context.Background(), &agentpublicv1.GetMyWorkAgentRequest{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.EnsureMyWorkAgent(context.Background(), &agentpublicv1.EnsureMyWorkAgentRequest{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.SendMessage(context.Background(), &agentpublicv1.SendMessageRequest{WorkId: "untrusted-session", InputId: "input", Text: "hello"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.RespondToInteraction(context.Background(), &agentpublicv1.RespondToInteractionRequest{WorkId: "untrusted-session", InteractionId: "question", Allow: true})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.ListPendingInteractions(context.Background(), &agentpublicv1.ListPendingInteractionsRequest{WorkId: "untrusted-session"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.CancelTurn(context.Background(), &agentpublicv1.CancelTurnRequest{WorkId: "untrusted-session", TurnId: "turn"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.GetWorkCapabilities(context.Background(), &agentpublicv1.GetWorkCapabilitiesRequest{WorkId: "untrusted-session"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.GetCurrentTurn(context.Background(), &agentpublicv1.GetCurrentTurnRequest{WorkId: "untrusted-session"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

type unusedExecutionPort struct{}

func (unusedExecutionPort) ListPendingInteractions(context.Context, string) ([]command.PendingInteraction, error) {
	panic("unauthenticated interaction query")
}

func (unusedExecutionPort) BufferInput(context.Context, string, command.ExecutionInput) (command.InputHandOff, error) {
	panic("unauthenticated input")
}
func (unusedExecutionPort) BufferResponse(context.Context, string, command.InteractionResponse) (command.InputHandOff, error) {
	panic("unauthenticated response")
}
func (unusedExecutionPort) ReadBuffer(context.Context, string) (command.BufferedInputs, error) {
	panic("unauthenticated buffer read")
}
func (unusedExecutionPort) DropBuffer(context.Context, string) error {
	panic("unauthenticated buffer drop")
}
func (unusedExecutionPort) StopExecution(context.Context, string, string) error {
	panic("unauthenticated stop")
}

// storageStub stands in for Storage's Sandbox Mount Contract: it grants each
// space by business identity and accepts every reclamation request. Storage's
// own behavior is verified in the Storage module.
func storageStub() fx.Option {
	return fx.Options(
		fx.Supply(agent.StorageConfig{Address: "http://storage.invalid", Token: "storage-test"}),
		fx.Supply(agent.UserDirectoryConfig{Address: "http://account.invalid"}),
		fx.Decorate(func(command.SandboxMountPort) command.SandboxMountPort { return stubStorage{} }),
		fx.Decorate(func(eventhandler.WorkspaceReclamationQueue) eventhandler.WorkspaceReclamationQueue {
			return stubStorage{}
		}),
	)
}

type stubStorage struct{}

func (stubStorage) AcquireMounts(_ context.Context, request domain.SandboxMountRequest) (domain.SandboxMounts, error) {
	mounts := domain.SandboxMounts{Workspace: &domain.MountGrant{ClaimName: "nas", SubPath: "workspaces/" + request.WorkspaceReference}, Home: domain.MountGrant{ClaimName: "nas", SubPath: "homes/" + request.HomeOwner}}
	if request.InputSource.Present() {
		mounts.Inputs = &domain.MountGrant{ClaimName: "inputs", SubPath: "attachments/" + request.InputSource.Reference, ReadOnly: true}
	}
	for _, mount := range request.ReadOnlyMounts {
		if mounts.ReadOnly == nil {
			mounts.ReadOnly = map[string]domain.MountGrant{}
		}
		mounts.ReadOnly[mount.Name] = domain.MountGrant{ClaimName: "inputs", SubPath: "references/" + mount.Reference, ReadOnly: true}
	}
	return mounts, nil
}

func (stubStorage) RequestWorkspaceReclamation(context.Context, string, string) error { return nil }

// Module tests use the real publication transport. Unauthenticated requests do
// not connect; lifecycle fixtures exercise current schema and real NATS wiring.
func sessionStateTransport(t *testing.T) fx.Option {
	t.Helper()
	serverURL := middlewaretest.Environment(t, "TALGENT_TEST_NATS_URL")
	return fx.Options(fx.Supply(http.DefaultClient, agent.PluginCatalogConfig{Address: "http://127.0.0.1:1"}, pkgnats.Config{URL: serverURL}, query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})), fx.Provide(pkgnats.NewLazyConn, func() query.WorkCredentialVerifier { return unusedExecutionPort{} }, func() query.CallPositionReader { return unusedExecutionPort{} }, func() message.Publisher { return discardedMessages{} }),
		// The Library reads' native callers are verified by the native API in
		// front; no test here reaches it.
		fx.Provide(func() (*nativeauth.LiveWorkClient, error) {
			return nativeauth.NewLiveWorkClient(http.DefaultClient, "http://agent.unused", "control")
		}))
}

// discardedMessages stands in for the Integration Message bus.
type discardedMessages struct{}

func (discardedMessages) Publish(context.Context, message.Message) error { return nil }

func (unusedExecutionPort) FindCallPosition(context.Context, string, string) (string, bool, error) {
	panic("unexpected context position query")
}

func (unusedExecutionPort) SetThinkingLevel(context.Context, string, string) error {
	panic("unauthenticated thinking setting")
}

func (unusedExecutionPort) SetPermissionMode(context.Context, string, domain.PermissionMode) (domain.PermissionMode, error) {
	panic("unauthenticated permission mode setting")
}

func (unusedExecutionPort) GetThinkingLevel(context.Context, string) (string, error) {
	panic("unauthenticated thinking setting query")
}

func (unusedExecutionPort) CancelTurn(context.Context, string, string) error {
	panic("unauthenticated cancellation")
}
func (unusedExecutionPort) GetCurrentTurn(context.Context, string) (command.CurrentTurn, error) {
	panic("unauthenticated turn query")
}

func (unusedExecutionPort) ObserveProcess(context.Context, string, string) (command.WorkProcessObservation, error) {
	panic("unexpected process observation")
}

func (unusedExecutionPort) VerifyWorkCredential(context.Context, string, string) error {
	panic("unexpected Work credential verification")
}

func (unusedExecutionPort) GetWorkCapabilities(context.Context, string) (command.WorkCapabilities, error) {
	panic("unexpected capability query")
}
