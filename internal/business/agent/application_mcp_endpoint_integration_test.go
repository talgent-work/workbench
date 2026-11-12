//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// perWorkKey is the credential an Application issues for one Work's calls to
// its own MCP server.
const perWorkKey = "Bearer desk-key-for-ticket-42"

func ticketTools() *agentpublicv2.ApplicationMcpEndpoint {
	return &agentpublicv2.ApplicationMcpEndpoint{Name: "tickets", Url: "https://desk.example/mcp", Headers: map[string]string{"Authorization": perWorkKey, "X-Desk-Tenant": "acme"}}
}

func (c ownerClients) startWith(requestID string, endpoints ...*agentpublicv2.ApplicationMcpEndpoint) (string, error) {
	response, err := c.works.StartWork(c.ctx, &agentpublicv2.StartWorkRequest{RequestId: requestID, InitialMessage: "Answer ticket 42", ApplicationMcpEndpoints: endpoints})
	return response.GetWorkId(), err
}

// An Application's own MCP endpoint reaches only the Work it was given to,
// with the headers the Application gave, unchanged across the Work's runs
// and a restart of the module; its other Works and no read of the Work see
// those headers.
func TestAnApplicationEndpointReachesOnlyItsWork(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk := m.as("application", supportDesk)

	given, err := desk.startWith(uuid.NewString(), ticketTools())
	require.NoError(t, err)
	plain, err := desk.startWith(uuid.NewString())
	require.NoError(t, err)

	want := []domain.ApplicationMCPEndpoint{{Name: "tickets", URL: "https://desk.example/mcp", Headers: map[string]string{"Authorization": perWorkKey, "X-Desk-Tenant": "acme"}}}
	m.runs.ready(t, m.ctx, given)
	require.Equal(t, want, m.runs.latest(t, m.ctx, given).Launch.Capabilities.ApplicationEndpoints, "the Work's run declares the endpoint as given")
	m.runs.ready(t, m.ctx, plain)
	require.Empty(t, m.runs.latest(t, m.ctx, plain).Launch.Capabilities.ApplicationEndpoints, "another Work of the Application never sees it")

	m.restart()
	retained, err := m.app.Works.Get(m.ctx, given)
	require.NoError(t, err)
	require.Equal(t, want, retained.CapabilitySnapshot.ApplicationEndpoints, "the headers stay unchanged for the Work's life")

	desk = m.as("application", supportDesk)
	view, err := desk.work.GetWork(desk.ctx, &agentpublicv1.GetWorkRequest{WorkId: given})
	require.NoError(t, err)
	listed, err := desk.works.ListWorks(desk.ctx, &agentpublicv2.ListWorksRequest{})
	require.NoError(t, err)
	for _, read := range []proto.Message{view, listed} {
		encoded, err := protojson.Marshal(read)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), perWorkKey, "no read of the Work returns the endpoint's headers")
	}
}

// Only an Application's Work takes Application MCP endpoints, and a
// malformed one is refused without echoing its headers.
func TestApplicationEndpointsAreRefusedOutsideTheirRules(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk, person := m.as("application", supportDesk), m.as("user", uuid.NewString())

	refused := func(err error) {
		t.Helper()
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		require.NotContains(t, err.Error(), perWorkKey)
	}
	_, err := person.startWith(uuid.NewString(), ticketTools())
	refused(err)

	insecure := ticketTools()
	insecure.Url = "https://user:pass@desk.example/mcp"
	unnamed := ticketTools()
	unnamed.Name = "-tickets"
	badHeader := ticketTools()
	badHeader.Headers["Bad Header"] = perWorkKey
	for _, endpoints := range [][]*agentpublicv2.ApplicationMcpEndpoint{{insecure}, {unnamed}, {badHeader}, {ticketTools(), ticketTools()}} {
		_, err = desk.startWith(uuid.NewString(), endpoints...)
		refused(err)
	}
	require.Empty(t, desk.listed(t), "a refused start leaves no Work")
}
