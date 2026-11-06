package connectorcatalogtest

import (
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Required supplies the platform Connector as Marketplace publishes it: the
// platform Connector over HTTP, in Agent-only tests.
func Required() domain.CatalogConnector {
	return domain.CatalogConnector{Offered: true, SelectedConnector: domain.SelectedConnector{ID: "01900000-0000-7000-8000-000000000382", Name: "talgent", Required: true, Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp", Authentication: "work_identity"}}
}

// GitHub supplies a controlled Provider-authenticated catalog entry.
func GitHub() domain.CatalogConnector {
	return domain.CatalogConnector{Offered: true, SelectedConnector: domain.SelectedConnector{ID: "01900000-0000-7000-8000-000000000386", Name: "github", Command: "github-mcp-server", Arguments: []string{"stdio"},
		Environment: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}, Authentication: "provider_authorization",
		Provider: "github", Target: "github-api", ProviderAuthentication: "credential_proxy"}}
}

// Selection supplies a catalog with only the required entry and no installations.
func Selection() command.ConnectorSelection {
	return command.ConnectorSelection{Catalog: []domain.CatalogConnector{Required()}}
}
