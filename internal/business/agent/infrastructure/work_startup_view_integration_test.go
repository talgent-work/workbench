//go:build integration

package infrastructure_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// An admitted Work's public view and its thinking level over the public API,
// real MySQL and real principals. Only execution delivery is controlled.
func TestStartingWorkViewAndThinkingLevel(t *testing.T) {
	const owner = "123456789012345680"
	f := newWorkDefinitionFixture(t, owner)
	ctx := context.Background()
	models := query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deepseek"}, Name: "DeepSeek", Vendor: "deepseek", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "max"}, query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "glm"}, Name: "GLM 5.3", Vendor: "zhipu"})
	var starts []domain.StartExecutionRequest
	create, _ := creationCoordinator(f.db, f.requests, &creationCapacity{}, &registrationResources{}, &starts, nil, models, &personalStartupCatalog{})
	id, err := create.Handle(ctx, f.request.Creation)
	require.NoError(t, err)
	require.Len(t, starts, 1)
	thinking := &workThinking{}
	owners := infrastructure.NewWorkAuthorizer(f.db)
	views := query.NewModelOptionViews(infrastructure.NewWorkQueryRepository(f.db), models)
	app := &application.Application{Works: f.sessions, Thinking: thinking, WorkPrincipals: owners, WorkViews: views, Models: models, Queries: application.Queries{
		GetWork:     query.NewGetWorkHandler(f.agents, f.sessions, owners, application.AssembleAgentWorkEntity, querytest.NoRequiredResponse),
		GetWorkView: query.NewGetWorkViewHandler(owners, views),
	}}
	handler := connecttest.Handler(func(s *connect.Server) {
		agentpublicv1connect.RegisterAgentWorkServiceHandler(s, transport.NewWorkHandler(app))
	}, authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))

	started, err := client.GetWork(workMessageRequest(ctx, owner), &agentpublicv1.GetWorkRequest{WorkId: id})
	require.NoError(t, err)
	require.Empty(t, started.Work.GetStartupRequest().Repositories[0].CheckoutStatus, "no outcome before workspace preparation ends")
	// A checkout cut off by the Startup Deadline is shown as not checked out.
	initial := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	retained, err := initial.Get(ctx, id)
	require.NoError(t, err)
	retained.CheckoutResults = []command.RepositoryCheckoutResult{{RepositoryID: "123", TargetPath: "/workspace/repos/project", Reason: "timed_out"}}
	retained.InputPrepared = true
	require.NoError(t, initial.Save(ctx, retained))
	degraded, err := client.GetWork(workMessageRequest(ctx, owner), &agentpublicv1.GetWorkRequest{WorkId: id})
	require.NoError(t, err)
	repository := degraded.Work.GetStartupRequest().Repositories[0]
	require.Equal(t, []string{"not_checked_out", "timed_out"}, []string{repository.CheckoutStatus, repository.CheckoutReason})

	setting := &agentpublicv1.SetThinkingLevelRequest{WorkId: id, Level: "high"}
	_, err = client.SetThinkingLevel(ctx, setting)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.SetThinkingLevel(workMessageRequest(ctx, messageCollaborator), setting)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = client.SetThinkingLevel(workMessageRequest(ctx, owner), setting)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a setting cannot start an execution")
	admitted, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, admitted.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, admitted.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, f.sessions.Save(ctx, admitted))
	setting.Level = "medium"
	_, err = client.SetThinkingLevel(workMessageRequest(ctx, owner), setting)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "valid native level can still be unsupported by the selected model")
	require.Empty(t, thinking.levels)
	setting.Level = "high"
	_, err = client.SetThinkingLevel(workMessageRequest(ctx, owner), setting)
	require.NoError(t, err)
	require.Equal(t, []string{"high"}, thinking.levels)
	require.Equal(t, id, thinking.ref)
	_, err = client.GetThinkingLevel(workMessageRequest(ctx, messageCollaborator), &agentpublicv1.GetThinkingLevelRequest{WorkId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "only the owner reads its Work settings")
	level, err := client.GetThinkingLevel(workMessageRequest(ctx, owner), &agentpublicv1.GetThinkingLevelRequest{WorkId: id})
	require.NoError(t, err)
	require.Equal(t, "high", level.Level)
	retainedWork, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkModelSelection{ConfigurationID: "deepseek"}, retainedWork.WorkModel)
	require.Equal(t, "high", retainedWork.ThinkingLevel)

	stopped, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	port := startupExecutionPort{}
	require.NoError(t, stopped.Stop(ctx, "thinking-stop", port))
	require.NoError(t, stopped.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, stopped.ConfirmComputeReclaimed())
	require.NoError(t, f.sessions.Save(ctx, stopped))
	level, err = client.GetThinkingLevel(workMessageRequest(ctx, owner), &agentpublicv1.GetThinkingLevelRequest{WorkId: id})
	require.NoError(t, err)
	require.Equal(t, "high", level.Level, "a stopped Work keeps its retained setting")
}

type workThinking struct {
	ref    string
	levels []string
}

func (s *workThinking) SetThinkingLevel(_ context.Context, ref string, level string) error {
	s.ref = ref
	s.levels = append(s.levels, level)
	return nil
}
