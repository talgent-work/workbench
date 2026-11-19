package domain

import "context"

type InstalledPluginRepository interface {
	Get(ctx context.Context, userID, slug string) (*InstalledPlugin, error)
	Save(ctx context.Context, plugin *InstalledPlugin) error
	Delete(ctx context.Context, plugin *InstalledPlugin) error
}

type ConnectorRepository interface {
	Get(ctx context.Context, id string) (*Connector, error)
	// GetPlatform finds a Platform Connector by its name.
	GetPlatform(ctx context.Context, name string) (*Connector, error)
	// Save keeps names unique per owner, reporting ErrConnectorNameTaken for
	// an owner's duplicate.
	Save(context.Context, *Connector) error
	// Delete removes a deleted Private Connector.
	Delete(context.Context, *Connector) error
}

type InstalledConnectorRepository interface {
	Get(ctx context.Context, userID, connectorID string) (*InstalledConnector, error)
	Save(ctx context.Context, installation *InstalledConnector) error
	Delete(ctx context.Context, installation *InstalledConnector) error
}
