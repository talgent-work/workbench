//go:build integration

package infrastructure_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	dispatchercommand "github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
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
	create := command.NewCreateWorkHandler(time.Now, definitions, works, works, infrastructure.NewWorkAuthorizer(db), creationModels(), &creationCatalog{}, creationConnections{}, works, computertest.Checked(&initialCreationCapacity{}), infrastructure.NewExecutionRequestsFactory(&creationExecution{db: db}))
	id, err := create.Handle(t.Context(), creationInput(definition.ID, "plugin-results"))
	require.NoError(t, err)
	work, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, work.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, works.Save(t.Context(), work))
	before, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	app := &application.Application{Works: works, WorkPrincipals: pluginResultAccess{workID: id}, PluginResults: infrastructure.NewPluginResults(db, time.Now)}
	mux := http.NewServeMux()
	path, handler := agentpublicv1connect.NewAgentWorkServiceHandler(transport.NewWorkHandler(app), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	mux.Handle(path, handler)
	path, handler = agentprivatev1connect.NewExecutionFeedbackServiceHandler(transport.NewFeedbackHandler(app, nil, "feedback-test"))
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentpublicv1connect.NewAgentWorkServiceClient(server.Client(), server.URL)
	read := func(actor, workID string) (*connect.Response[agentpublicv1.GetWorkPluginResultsResponse], error) {
		request := connect.NewRequest(&agentpublicv1.GetWorkPluginResultsRequest{WorkId: workID})
		if actor != "" {
			request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
			request.Header().Set(authn.HeaderSubject, actor)
		}
		return client.GetWorkPluginResults(t.Context(), request)
	}
	_, err = read("", id)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = read("bfe6534f-df01-5e55-b980-d252c7563446", id)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", "another-work")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	response, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Len(t, response.Msg.Plugins, 1)
	result := response.Msg.Plugins[0]
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
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_SUCCEEDED, response.Msg.Plugins[0].Loading)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_UNSPECIFIED, response.Msg.Plugins[0].Installation, "loading feedback does not invent installation observations")
	// A lower sequence is allowed to replace the projection, without a run fence.
	observation.ObservationSequence, observation.LoadedPlugins = 1, nil
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, "not_loaded", response.Msg.Plugins[0].LoadingReason)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_FAILED, response.Msg.Plugins[0].Loading)
	unauthorized := agentfeedback.NewClient(server.Client(), server.URL, "wrong")
	observation.LoadedPlugins = []dispatchercommand.LoadedPlugin{{Slug: "runtime", Version: "2"}}
	require.Error(t, unauthorized.ReportExecution(t.Context(), observation))
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, "version_mismatch", response.Msg.Plugins[0].LoadingReason)
	observation.LoadedPlugins[0].Version = "1"
	require.NoError(t, feedback.ReportExecution(t.Context(), observation))
	// Reject malformed feedback atomically instead of replacing existing facts.
	rpc := agentprivatev1connect.NewExecutionFeedbackServiceClient(server.Client(), server.URL)
	bad := connect.NewRequest(&agentprivatev1.ReportRequest{WorkId: id, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_PLUGIN_LOADS_OBSERVED, LoadedPlugins: []*agentprivatev1.LoadedPlugin{{Slug: "runtime"}, {Slug: "runtime"}}})
	bad.Header().Set("Authorization", "Bearer feedback-test")
	_, err = rpc.Report(t.Context(), bad)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	app.PluginResults = infrastructure.NewPluginResults(db, time.Now)
	response, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.NoError(t, err)
	require.Equal(t, agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_SUCCEEDED, response.Msg.Plugins[0].Loading)
	retained, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, retained.State, "a missing required plugin does not itself fail a usable Work")
	require.Equal(t, before, retained, "projection feedback cannot mutate the Work or fixed release")
	_, err = db.Exec("RENAME TABLE agent_plugin_result TO unavailable_plugin_result")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.Exec("RENAME TABLE unavailable_plugin_result TO agent_plugin_result")
		require.NoError(t, err)
	})
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", id)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err), "unavailable results are not an empty successful query")
}

// A rejected inventory update must leave the previous complete observation visible.
func TestPluginLoadInventoryRejectsPartialReplacement(t *testing.T) {
	f := newWorkCreationFixture(t)
	store := infrastructure.NewPluginResults(f.db, time.Now)
	require.NoError(t, store.RecordLoads(t.Context(), "work", []application.PluginLoadResult{{Slug: "retained", Loaded: true}}))
	before, err := store.Read(t.Context(), "work")
	require.NoError(t, err)
	require.Error(t, store.RecordLoads(t.Context(), "work", []application.PluginLoadResult{{Slug: "retained", Loaded: false}, {Slug: strings.Repeat("x", 129), Loaded: true}}))
	after, err := store.Read(t.Context(), "work")
	require.NoError(t, err)
	require.Equal(t, before, after)
}
