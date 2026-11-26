package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// Only an Owner reads or writes a Work Agent's Workspace Instructions through
// the public API; the signed-in person comes from the trusted gateway, never
// the request.
func TestWorkspaceInstructionsAreReadAndWrittenOnlyByAnOwner(t *testing.T) {
	agents := &rpcAgents{items: map[string]*domain.AgentDefinition{}}
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(context.Background(), definition))
	app := &application.Application{Definitions: agents, Logger: slog.Default(),
		Commands: application.Commands{ChangeAgentWorkspaceInstructions: command.NewChangeAgentWorkspaceInstructionsHandler(agents)},
		Queries:  application.Queries{GetAgentWorkspaceInstructions: query.NewGetAgentWorkspaceInstructionsHandler(agents)},
	}
	server := connecttest.NewServer()
	agentmodule.RegisterRPCHandlers(server, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, nil)
	address := connecttest.Start(t, server)
	public := agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, address, connecthttp.WithReadMaxBytes(0))))
	ctx := context.Background()
	read := func(ctx context.Context) (*agentpublicv1.GetAgentWorkspaceInstructionsResponse, error) {
		return public.GetAgentWorkspaceInstructions(ctx, &agentpublicv1.GetAgentWorkspaceInstructionsRequest{AgentId: definition.ID})
	}
	change := func(ctx context.Context, instructions string) (*agentpublicv1.ChangeAgentWorkspaceInstructionsResponse, error) {
		return public.ChangeAgentWorkspaceInstructions(ctx, &agentpublicv1.ChangeAgentWorkspaceInstructionsRequest{AgentId: definition.ID, Instructions: instructions})
	}

	_, err = read(ctx)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = change(ctx, "x")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = read(asUser(ctx, "mallory"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = change(asUser(ctx, "mallory"), "Leak.")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = change(asUser(ctx, "alice"), strings.Repeat("a", domain.MaxWorkspaceInstructionsRunes+1))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	changed, err := change(asUser(ctx, "alice"), "  Run make check.  ")
	require.NoError(t, err)
	require.Equal(t, "Run make check.", changed.GetInstructions())
	saved, err := read(asUser(ctx, "alice"))
	require.NoError(t, err)
	require.Equal(t, "Run make check.", saved.GetInstructions())
}
