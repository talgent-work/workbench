package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// A context admitting an Agent's write asks Agent whether the Work may act
// now: a Working Work whose person owes no response acts; a Work that is not
// Working, or awaits its person, is refused, and an unknown Work is not found.
func TestActingWorkAdmitsAnAgentActionOnlyWhileItsWorkActs(t *testing.T) {
	ctx := context.Background()
	agentID := uuid.NewString()
	work, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", WorkCaller: domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "owner"}, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), AgentID: agentID, RepresentedPerson: "home", WorkLayout: domain.DefaultWorkLayout()}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	works := &rpcSessions{items: map[string]*domain.AgentWork{}}
	require.NoError(t, works.Save(ctx, work))
	awaiting := false
	app := &application.Application{Queries: application.Queries{AdmitAgentAction: query.NewAdmitAgentActionHandler(works, func(context.Context, string) (bool, error) { return awaiting, nil })}}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "integration", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewActingWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	admit := func(token, workID string) error {
		_, err := client.AdmitAgentAction(authenticated(ctx, token), &agentprivatev1.AdmitAgentActionRequest{WorkId: workID})
		return err
	}

	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(admit("feedback", work.ID)), "only a trusted context asks")
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(admit("integration", work.ID)), "a Work not yet Working does not act")
	work.State = domain.WorkWorking
	require.NoError(t, works.Save(ctx, work))
	require.NoError(t, admit("integration", work.ID), "a Working Work owing nothing acts")
	awaiting = true
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(admit("integration", work.ID)), "a Work awaiting its person does not act")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(admit("integration", uuid.NewString())))
}
