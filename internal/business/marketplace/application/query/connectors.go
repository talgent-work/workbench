package query

import "context"

// CatalogConnector is a credential-free read of one platform catalog entry.
type CatalogConnector struct {
	ID, Name, Description, Category string
	Required                        bool
	// Offered reports whether new installations and Work selections may use it.
	Offered        bool
	Authentication string
	Configuration  ConnectorConfiguration
	// Provider is present only for Provider-authenticated entries.
	Provider *ConnectorProvider
}

type ConnectorConfiguration struct {
	Type, URL, Command   string
	Arguments            []string
	Environment, Headers map[string]string
}

type ConnectorProvider struct{ Provider, Target, Authentication string }

// InstalledConnector is one personal installation of a catalog entry.
type InstalledConnector struct {
	ConnectorID string
	Enabled     bool
	Version     int
}

// ConnectorQueryRepository lists the whole Connector catalog, which Get by
// identity does not serve.
type ConnectorQueryRepository interface {
	ListCatalog(ctx context.Context) ([]CatalogConnector, error)
}

// InstalledConnectorQueryRepository lists an owner's installed Connectors, a
// person's or an Application's.
type InstalledConnectorQueryRepository interface {
	ListInstalled(ctx context.Context, ownerID string) ([]InstalledConnector, error)
}
