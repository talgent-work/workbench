package agenttest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	marketapp "github.com/talgent-work/workbench/internal/business/marketplace/application"
	marketdomain "github.com/talgent-work/workbench/internal/business/marketplace/domain"
	marketinfra "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	markettransport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// CloudLaunch is the Harness launch of a Work as a Cloud sandbox runs it.
type CloudLaunch = execution.CloudLaunch

// PlatformConnectorLaunch renders the Harness launch of the Work workID's
// fresh conversation in a Cloud sandbox, the Harness at binary, whose only
// Connector is the platform Connector at mcpAddress as the Marketplace over the
// MySQL schema at dsn publishes and selects it. The Work runs no startup Agent
// and no Plugins. Its sandbox's Credential Proxy, which adds the Work
// credential to every request to the Connector, is the caller's.
func PlatformConnectorLaunch(t testing.TB, dsn, binary, workID, mcpAddress string) CloudLaunch {
	t.Helper()
	request := domain.StartExecutionRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, WorkID: workID, ContextMode: domain.ContextFresh,
		HarnessSessionReference: "context", WorkspaceReference: "workspace", HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"},
		Capabilities: domain.CapabilitySnapshot{Prompt: "fixed prompt\n", Connectors: []domain.SelectedConnector{platformConnector(t, dsn, mcpAddress)}}}
	launch, err := (execution.NativeLaunchConfig{Binary: binary}).CloudLaunch(request, execution.Allocation{WorkID: workID, ID: "01900000-0000-7000-8000-000000000001"})
	require.NoError(t, err)
	return launch
}

// platformConnector publishes the platform Connector at address in the
// Marketplace and selects it as Agent does. The Work credential is never part
// of the Connector record.
func platformConnector(t testing.TB, dsn, address string) domain.SelectedConnector {
	t.Helper()
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	connectors := marketinfra.NewConnectorRepository(db)
	app := marketapp.NewApplication(nil, nil, nil, nil, nil, marketapp.ConnectorRepositories{Catalog: connectors, Installed: marketinfra.NewInstalledConnectorRepository(db), Queries: connectors})
	ctx := context.Background()
	require.NoError(t, app.PublishConnector(ctx, marketapp.ConnectorPublication{Name: "talgent", Configuration: marketdomain.MCPConfiguration{Type: "http", URL: address}}))
	mux := http.NewServeMux()
	mux.Handle(marketplaceprivatev1connect.NewSelectionServiceHandler(markettransport.NewSelectionHandler(app, "fixture-token"), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "fixture-token"})
	require.NoError(t, err)
	selection, err := client.ReadConnectorSelection(ctx, "owner")
	require.NoError(t, err)
	require.Len(t, selection.Catalog, 1)
	return selection.Catalog[0].SelectedConnector
}
