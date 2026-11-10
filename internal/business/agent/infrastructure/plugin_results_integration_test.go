//go:build integration

package infrastructure_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	dispatchercommand "github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

type pluginResultAccess struct{ workID string }

// pluginResultAccess admits only the Work owner, as the gateway authenticated it.
func (a pluginResultAccess) AuthorizeWork(_ context.Context, actor, workID string) error {
	if actor != "0ba1b686-0dfe-53b5-9f82-930f94137cd8" || workID != a.workID {
		return access.ErrAccessDenied
	}
	return nil
}

func TestPluginResultsThroughAuthenticatedPublicAndFeedbackAPIs(t *testing.T) {
	db, definitions, works, definition := startingCreationDatabase(t)
	create := command.NewCreateWorkHandler(time.Now, definitions, works, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(&initialCreationCapacity{}), creationPreparation(db, creationModels()))
	id, err := create.Handle(t.Context(), creationInput(definition.ID, "plugin-results"))
	require.NoError(t, err)
	work, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	// The Work's current run launched the process "process" and became ready.
	run := newRuns(t, db, works)
	run.launch(t.Context(), id)
	work, err = works.Get(t.Context(), id)
	require.NoError(t, err)
	reportReady(t, work)
	require.NoError(t, works.Save(t.Context(), work))
	before, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	app := &application.Application{Works: works, WorkPrincipals: pluginResultAccess{workID: id}, ExecutionQueries: execution.NewExecutionQueryRepository(db),
		Commands: application.Commands{ApplyExecutionFeedback: run.feedback}}
	mux := http.NewServeMux()
	path, handler := "/"+agentpublicv1connect.AgentWorkServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		agentpublicv1connect.RegisterAgentWorkServiceHandler(s, transport.NewWorkHandler(app))
	}, authn.NewPrincipalInterceptor())
	mux.Handle(path, handler)
	path, handler = "/"+agentprivatev1connect.ExecutionFeedbackServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(s, transport.NewFeedbackHandler(app, nil, "feedback-test"))
	})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	read := func(actor, workID string) (*agentpublicv1.GetWorkPluginResultsResponse, error) {
		request := &agentpublicv1.GetWorkPluginResultsRequest{WorkId: workID}
		ctx := t.Context()
		if actor != "" {
			var info *connect.CallInfo
			ctx, info = connect.NewClientContext(ctx)
			info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
			info.RequestHeader().Set(authn.HeaderPrincipal, "user")
			info.RequestHeader().Set(authn.HeaderSubject, actor)
		}
		return client.GetWorkPluginResults(ctx, request)
	}
	_, err = read("", id)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = read("bfe6534f-df01-5e55-b980-d252c7563446", id)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", "another-work")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	response, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Len(t, response.Plugins, 1)
	result := response.Plugins[0]
	require.Equal(t, "runtime", result.Slug)
	require.Equal(t, "1", result.ReleaseVersion)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_UNSPECIFIED, result.Installation)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_UNSPECIFIED, result.Loading)
	require.Nil(t, result.UpdatedAt)
	feedback := agentfeedback.NewClient(server.Client(), server.URL, "feedback-test")
	observation := dispatchercommand.ExecutionObservation{WorkID: id, ProcessID: "process", Kind: dispatchercommand.ExecutionPluginLoadsObserved, ObservationSequence: 100, LoadedPlugins: []dispatchercommand.LoadedPlugin{{Slug: "runtime", Version: "1"}}}
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_SUCCEEDED, response.Plugins[0].Loading)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_UNSPECIFIED, response.Plugins[0].Installation, "loading feedback does not invent installation observations")
	// A later report replaces the outcome, whatever its stream position.
	observation.ObservationSequence, observation.LoadedPlugins = 1, nil
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, "not_loaded", response.Plugins[0].LoadingReason)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_FAILED, response.Plugins[0].Loading)
	unauthorized := agentfeedback.NewClient(server.Client(), server.URL, "wrong")
	observation.LoadedPlugins = []dispatchercommand.LoadedPlugin{{Slug: "runtime", Version: "2"}}
	require.Error(t, unauthorized.ReportExecution(t.Context(), observation))
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, "version_mismatch", response.Plugins[0].LoadingReason)
	observation.LoadedPlugins[0].Version = "1"
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	// Reject malformed feedback atomically instead of replacing existing facts.
	rpc := agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	bad := &agentprivatev1.ReportRequest{WorkId: id, ProcessId: "process", Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_PLUGIN_LOADS_OBSERVED, LoadedPlugins: []*agentprivatev1.LoadedPlugin{{Slug: "runtime"}, {Slug: "runtime"}}}
	badCtx, badInfo := connect.NewClientContext(t.Context())
	badInfo.RequestHeader().Set("Authorization", "Bearer feedback-test")
	_, err = rpc.Report(badCtx, bad)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_SUCCEEDED, response.Plugins[0].Loading)
	retained, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, retained.State, "a missing required plugin does not itself fail a usable Work")
	require.Equal(t, before, retained, "loading feedback cannot mutate the Work or fixed release")
	// Feedback from an earlier start's process changes no outcome of the
	// current run.
	earlier := dispatchercommand.ExecutionObservation{WorkID: id, ProcessID: "earlier", Kind: dispatchercommand.ExecutionPluginLoadsObserved, ObservationSequence: 200}
	require.NoError(t, feedback.ReportExecution(t.Context(), earlier))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_SUCCEEDED, response.Plugins[0].Loading)
	_, err = db.Exec("RENAME TABLE execution_plugin_result TO unavailable_plugin_result")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.Exec("RENAME TABLE unavailable_plugin_result TO execution_plugin_result")
		require.NoError(t, err)
	})
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err), "unavailable results are not an empty successful query")
}
