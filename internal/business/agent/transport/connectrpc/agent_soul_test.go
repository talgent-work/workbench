package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Only an Owner reads or writes a Work Agent's Soul through the public API;
// the signed-in person comes from the trusted gateway, never the request.
func TestSoulIsReadAndWrittenOnlyByAnOwner(t *testing.T) {
	agents := &rpcAgents{items: map[string]*domain.AgentDefinition{}}
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}, HomeBinding: domain.HomeBinding{OwnerID: "alice", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(context.Background(), definition))
	app := &application.Application{Definitions: agents, Logger: slog.Default(),
		Commands: application.Commands{ChangeAgentSoul: command.NewChangeAgentSoulHandler(agents)},
		Queries:  application.Queries{GetAgentSoul: query.NewGetAgentSoulHandler(agents)},
	}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	public := agentpublicv1connect.NewAgentWorkServiceClient(server.Client(), server.URL)
	ctx := context.Background()

	_, err = public.GetAgentSoul(ctx, connect.NewRequest(&agentpublicv1.GetAgentSoulRequest{AgentId: definition.ID}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.ChangeAgentSoul(ctx, connect.NewRequest(&agentpublicv1.ChangeAgentSoulRequest{AgentId: definition.ID, Soul: "x"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = public.GetAgentSoul(ctx, asUser("mallory", &agentpublicv1.GetAgentSoulRequest{AgentId: definition.ID}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = public.ChangeAgentSoul(ctx, asUser("mallory", &agentpublicv1.ChangeAgentSoulRequest{AgentId: definition.ID, Soul: "Leak."}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = public.ChangeAgentSoul(ctx, asUser("alice", &agentpublicv1.ChangeAgentSoulRequest{AgentId: definition.ID, Soul: strings.Repeat("a", domain.MaxSoulRunes+1)}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	changed, err := public.ChangeAgentSoul(ctx, asUser("alice", &agentpublicv1.ChangeAgentSoulRequest{AgentId: definition.ID, Soul: "  Be terse.  "}))
	require.NoError(t, err)
	require.Equal(t, "Be terse.", changed.Msg.GetSoul())
	read, err := public.GetAgentSoul(ctx, asUser("alice", &agentpublicv1.GetAgentSoulRequest{AgentId: definition.ID}))
	require.NoError(t, err)
	require.Equal(t, "Be terse.", read.Msg.GetSoul())
}
