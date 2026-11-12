//go:build integration

package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestServiceAccessResultsThroughAuthenticatedPublicAPI(t *testing.T) {
	f := newNativeWorkLifecycle(t, t.Context())
	work, _ := f.createWithAuthorization(t, t.Context(), domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, domain.WorkAuthorizationInput{OwnerID: "lifecycle-owner", Available: []domain.PersonalAuthorization{{OwnerID: "lifecycle-owner", AuthorizationID: uuid.NewString(), Provider: "github", Service: "github"}}})
	app := &application.Application{Works: f.works, WorkPrincipals: serviceObservationAccess{workID: work.ID}, ExecutionQueries: NewExecutionQueryRepository(f.db)}
	mux := http.NewServeMux()
	mux.Handle("/"+agentpublicv1connect.AgentWorkServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		agentpublicv1connect.RegisterAgentWorkServiceHandler(s, transport.NewWorkHandler(app))
	}, authn.NewPrincipalInterceptor()))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	read := func(actor, workID string) (*agentpublicv1.GetWorkServiceAccessResponse, error) {
		request := &agentpublicv1.GetWorkServiceAccessRequest{WorkId: workID}
		ctx := t.Context()
		if actor != "" {
			var info *connect.CallInfo
			ctx, info = connect.NewClientContext(ctx)
			info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
			info.RequestHeader().Set(authn.HeaderPrincipal, "user")
			info.RequestHeader().Set(authn.HeaderSubject, actor)
		}
		return client.GetWorkServiceAccess(ctx, request)
	}
	_, err := read("", work.ID)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = read("bea682bb-1121-576c-82f4-c9a39d76f9d1", work.ID)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = read("e5968ce5-e1db-58d9-b31d-39cc8a1935cf", uuid.NewString())
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	response, err := read("e5968ce5-e1db-58d9-b31d-39cc8a1935cf", work.ID)
	require.NoError(t, err)
	require.Equal(t, "lifecycle-owner", response.OwnerUserId, "viewer never substitutes their own authorization")
	require.Equal(t, []string{"github"}, response.SelectedServices)
	require.Empty(t, response.Observations, "no observation must not claim authentication success")
	allocation, _, err := latestAllocation(t.Context(), f.db, work.ID)
	require.NoError(t, err)
	_, err = f.db.Exec("UPDATE agent_execution SET service_access_json = ? WHERE id = ?", `[{"service":"github","target":"github-remote-mcp","plugin":"tools","mcp":"github","status":"unavailable","reason":"reconnect_required"}]`, allocation.ID)
	require.NoError(t, err)
	response, err = read("e5968ce5-e1db-58d9-b31d-39cc8a1935cf", work.ID)
	require.NoError(t, err)
	require.Len(t, response.Observations, 1)
	observation := response.Observations[0]
	require.Equal(t, "github", observation.Service)
	require.Equal(t, "github-remote-mcp", observation.Target)
	require.Equal(t, "tools", observation.Plugin)
	require.Equal(t, "github", observation.Mcp)
	require.Equal(t, "unavailable", observation.Status)
	require.Equal(t, "reconnect_required", observation.Reason)

	// History remains readable after retirement, but a new allocation without
	// observations must not inherit a previous allocation's authentication result.
	_, err = f.db.Exec("UPDATE agent_execution SET deleted_at = updated_at WHERE id = ?", allocation.ID)
	require.NoError(t, err)
	response, err = read("e5968ce5-e1db-58d9-b31d-39cc8a1935cf", work.ID)
	require.NoError(t, err)
	require.Len(t, response.Observations, 1)
	allocation.ID = uuid.NewString()
	allocation.CreatedAt++
	allocation.ServiceAccessJSON = []byte("[]")
	_, err = f.db.Insert(&allocation)
	require.NoError(t, err)
	response, err = read("e5968ce5-e1db-58d9-b31d-39cc8a1935cf", work.ID)
	require.NoError(t, err)
	require.Empty(t, response.Observations)
	require.Equal(t, []string{"github"}, response.SelectedServices)
}

type serviceObservationAccess struct{ workID string }

// serviceObservationAccess admits only the Work owner, as the gateway authenticated it.
func (a serviceObservationAccess) AuthorizeWork(_ context.Context, actor, workID string) error {
	if actor != "e5968ce5-e1db-58d9-b31d-39cc8a1935cf" || workID != a.workID {
		return access.ErrAccessDenied
	}
	return nil
}
