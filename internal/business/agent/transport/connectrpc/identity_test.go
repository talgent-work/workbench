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
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// An Agent asks who it is through the entry and learns its Work's fixed
// facts from its own identity; a person is refused.
func TestAnAgentAsksWhoItIs(t *testing.T) {
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, &application.Application{}, agentmodule.RPCConfig{ControlToken: "integration", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentpublicv1connect.NewIdentityServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	const work = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4d"
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
		authn.HeaderSubject: work, authn.HeaderProjectID: "project", authn.HeaderAgentID: "definition", authn.HeaderPurpose: "work",
		authn.HeaderWorkSubjectKind: "case", authn.HeaderWorkSubjectID: "case-1",
	}), &agentpublicv1.WhoAmIRequest{})
	require.NoError(t, err)
	require.True(t, proto.Equal(&agentpublicv1.WhoAmIResponse{WorkId: work, WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: "project"}, WorkKey: &agentpublicv1.WorkKey{Kind: "case", Id: "case-1"}, Purpose: "work", AgentId: "definition"}, me), me.String())

	person := "2c857667-a533-5efc-b6da-fc9cce51d31b"
	_, err = client.WhoAmI(as("user", map[string]string{authn.HeaderSubject: person}), &agentpublicv1.WhoAmIRequest{})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
