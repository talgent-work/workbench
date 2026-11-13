package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// originRepository answers the origins of known Works; nothing else is read.
type originRepository struct {
	query.WorkQueryRepository
	origins map[string]query.WorkOrigin
}

func (r originRepository) GetWorkOrigin(_ context.Context, workID string) (query.WorkOrigin, error) {
	origin, ok := r.origins[workID]
	if !ok {
		return query.WorkOrigin{}, domain.ErrWorkNotFound
	}
	return origin, nil
}

// An Agent asks who it is through the entry and learns the facts its Work
// fixed at creation from Agent, whatever else its identity headers carry;
// every Work has a Work Caller, and a person is refused.
func TestAnAgentAsksWhoItIs(t *testing.T) {
	const work, standalone = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4d", "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"
	app := &application.Application{}
	app.Queries.GetWorkOrigin = query.NewGetWorkOriginHandler(originRepository{origins: map[string]query.WorkOrigin{
		work:       {WorkID: work, AgentID: "definition", Purpose: "work", WorkCaller: domain.WorkCaller{Kind: "project", ID: "p1"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case 1"}, FileHome: domain.FileHome{Kind: "case", ID: "case 1"}},
		standalone: {WorkID: standalone, AgentID: "definition", Purpose: "work", WorkCaller: domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "jack"}},
	}})
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "integration", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentpublicv1connect.NewIdentityServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	as := func(principal string, claims map[string]string) context.Context {
		ctx, info := connect.NewClientContext(context.Background())
		info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
		info.RequestHeader().Set(authn.HeaderPrincipal, principal)
		for key, value := range claims {
			info.RequestHeader().Set(key, value)
		}
		return ctx
	}

	me, err := client.WhoAmI(as("agent", map[string]string{
		authn.HeaderSubject: work, authn.HeaderProjectID: "stale", authn.HeaderAgentID: "definition", authn.HeaderPurpose: "work",
		authn.HeaderWorkSubjectKind: "stale", authn.HeaderWorkSubjectID: "stale",
	}), &agentpublicv1.WhoAmIRequest{})
	require.NoError(t, err)
	require.True(t, proto.Equal(&agentpublicv1.WhoAmIResponse{WorkId: work, WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: "p1"}, WorkKey: &agentpublicv1.WorkKey{Kind: "case", Id: "case 1"}, FileHome: &agentpublicv1.FileHome{Kind: "case", Id: "case 1"}, Purpose: "work", AgentId: "definition"}, me), me.String())

	own, err := client.WhoAmI(as("agent", map[string]string{authn.HeaderSubject: standalone, authn.HeaderAgentID: "definition", authn.HeaderPurpose: "work"}), &agentpublicv1.WhoAmIRequest{})
	require.NoError(t, err)
	require.True(t, proto.Equal(&agentpublicv1.WhoAmIResponse{WorkId: standalone, WorkCaller: &agentpublicv1.WorkCaller{Kind: "person", Id: "jack"}, Purpose: "work", AgentId: "definition"}, own), "a Standalone Work names its owner as Work Caller: %s", own.String())

	person := "2c857667-a533-5efc-b6da-fc9cce51d31b"
	_, err = client.WhoAmI(as("user", map[string]string{authn.HeaderSubject: person}), &agentpublicv1.WhoAmIRequest{})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
