package domain

import "context"

type InstalledPluginRepository interface {
	Get(ctx context.Context, userID, slug string) (*InstalledPlugin, error)
	Save(ctx context.Context, plugin *InstalledPlugin) error
	Delete(ctx context.Context, plugin *InstalledPlugin) error
}

type ConnectorRepository interface {
	GetByName(context.Context, string) (*Connector, error)
	// List loads the whole platform catalog, which is small and platform-owned.
	List(context.Context) ([]*Connector, error)
	Save(context.Context, *Connector) error
}

type InstalledConnectorRepository interface {
	Get(ctx context.Context, userID, connectorID string) (*InstalledConnector, error)
	Save(ctx context.Context, installation *InstalledConnector) error
	Delete(ctx context.Context, installation *InstalledConnector) error
}
