package query

import "context"

// CatalogConnector is a credential-free read of one Connector.
type CatalogConnector struct {
	ID, Name, Description, Category string
	Owner                           ConnectorOwner
	// State is private, published or withdrawn.
	State          string
	Required       bool
	Authentication string
	Configuration  ConnectorConfiguration
	// Provider is present only for Provider-authenticated entries.
	Provider *ConnectorProvider
	// IconDigest identifies the icon image; empty without one.
	IconDigest string
	Version    int
	// PublishedAtMillis is zero while Private.
	PublishedAtMillis int64
}

// ConnectorOwner names who owns a Connector.
type ConnectorOwner struct {
	// Kind is platform, person or application.
	Kind string
	// ID is empty for the platform.
	ID string
}

const (
	ConnectorPrivate   = "private"
	ConnectorPublished = "published"
	ConnectorWithdrawn = "withdrawn"

	OwnerPlatform    = "platform"
	OwnerPerson      = "person"
	OwnerApplication = "application"
)

// InCatalogOf reports whether the Connector is in the owner's Connector
// catalog: Published, or the owner's own Private one.
func (c CatalogConnector) InCatalogOf(owner string) bool {
	return c.State == ConnectorPublished || (c.State == ConnectorPrivate && c.OwnedBy(owner))
}

// OwnedBy reports whether owner made the Connector.
func (c CatalogConnector) OwnedBy(owner string) bool {
	return c.Owner.Kind != OwnerPlatform && owner != "" && c.Owner.ID == owner
}

type ConnectorConfiguration struct {
	Type, URL, Command   string
	Arguments            []string
	Environment, Headers map[string]string
}

type ConnectorProvider struct {
	Provider, Target, Authentication string
	// TargetCredentialsExpire reports the declared Provider's expiry.
	TargetCredentialsExpire bool
}

// InstalledConnector is one personal installation of a catalog entry.
type InstalledConnector struct {
	ConnectorID string
	Enabled     bool
	Version     int
}

// ConnectorQueryRepository reads Connector facts that Get by identity does
// not serve.
type ConnectorQueryRepository interface {
	// ListForOwner reads the owner's Connector catalog together with every
	// Connector the owner made and every Withdrawn one the owner installed.
	ListForOwner(ctx context.Context, owner string) ([]CatalogConnector, error)
	// ListPublished reads the Published Connectors, the Platform Connectors
	// among them, which every owner sees.
	ListPublished(ctx context.Context) ([]CatalogConnector, error)
}

// InstalledConnectorQueryRepository lists an owner's installed Connectors, a
// person's or an Application's.
type InstalledConnectorQueryRepository interface {
	ListInstalled(ctx context.Context, ownerID string) ([]InstalledConnector, error)
}
