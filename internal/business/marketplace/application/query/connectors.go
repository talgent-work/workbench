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

type ConnectorQueryRepository interface {
	ListCatalog(ctx context.Context) ([]CatalogConnector, error)
	ListInstalled(ctx context.Context, userID string) ([]InstalledConnector, error)
}
