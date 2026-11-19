package domain

import (
	"errors"
	"net/url"
	"slices"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/google/uuid"
)

var (
	// ErrConnectorNameTaken reports an owner who already holds a Connector of
	// the name (Unique Connector name).
	ErrConnectorNameTaken = errors.New("marketplace: the owner already has a Connector of this name")
	// ErrReservedConnectorName reports a Platform Connector's name (Reserved
	// platform names).
	ErrReservedConnectorName = errors.New("marketplace: the name belongs to a Platform Connector")
	// ErrProviderConnectorNotRemote reports a Provider Connector that is not a
	// remote HTTP MCP server (Connector configuration coherence).
	ErrProviderConnectorNotRemote = errors.New("marketplace: a Connector that signs in with a Provider is a remote server")
)

const EventKindPrivateConnectorDeleted domainevent.Kind = "marketplace.connector.private_deleted"

// PrivateConnectorDeleted records that an owner deleted a Private Connector,
// whose installation by that owner the Policy removes.
type PrivateConnectorDeleted struct {
	ConnectorID string
	OwnerID     string
	OccurredAt  time.Time
}

func (PrivateConnectorDeleted) Kind() domainevent.Kind { return EventKindPrivateConnectorDeleted }
func (e PrivateConnectorDeleted) Key() string          { return e.ConnectorID }

// ConnectorAddition is what an owner supplies to add a Connector. Provider is
// the deployment's declaration of the Provider it signs in with, if any.
type ConnectorAddition struct {
	Name          string
	Configuration MCPConfiguration
	Presentation  CatalogPresentation
	Provider      *ProviderDeclaration
}

// AddConnector establishes an owner's Connector as Private. It requires no
// personal Identity or credential and installs nothing; the supplied
// Platform Connector names are reserved. The Repository keeps names unique
// per owner.
func AddConnector(owner ConnectorOwner, addition ConnectorAddition, platformNames []string) (*Connector, error) {
	if owner.Platform() {
		return nil, ErrInvalidConnector
	}
	if !pluginSlug.MatchString(addition.Name) || len(addition.Name) > 128 {
		return nil, ErrInvalidConnector
	}
	if slices.Contains(platformNames, addition.Name) {
		return nil, ErrReservedConnectorName
	}
	c := &Connector{ID: uuid.Must(uuid.NewV7()).String(), Owner: owner, Name: addition.Name, Presentation: addition.Presentation,
		Configuration: addition.Configuration.Clone(), Authentication: NoAuthentication, State: ConnectorPrivate}
	if addition.Provider != nil {
		binding, err := bindProvider(c.Configuration, *addition.Provider)
		if err != nil {
			return nil, err
		}
		c.ProviderAccessBinding, c.Authentication = &binding, ProviderAuthorizationAuthentication
	}
	return c, c.Validate()
}

// Change replaces an owner-added Connector's MCPConfiguration and
// CatalogPresentation on its owner's command, keeping its name and declared
// Provider, whose supplied declaration rechecks target containment.
func (c *Connector) Change(caller string, configuration MCPConfiguration, presentation CatalogPresentation, provider *ProviderDeclaration) error {
	if err := c.ownerCommand(caller); err != nil {
		return err
	}
	if c.State == ConnectorWithdrawn {
		return ErrInvalidTransition
	}
	candidate := *c
	candidate.Configuration, candidate.Presentation = configuration.Clone(), presentation
	if c.ProviderAccessBinding != nil {
		if provider == nil || provider.Name != Provider(c.ProviderAccessBinding.Provider) {
			return ErrInvalidConnector
		}
		binding, err := bindProvider(candidate.Configuration, *provider)
		if err != nil {
			return err
		}
		candidate.ProviderAccessBinding = &binding
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*c = candidate
	return nil
}

// Publish offers a Private Connector to every owner on its owner's command.
func (c *Connector) Publish(caller string, now time.Time) error {
	if err := c.ownerCommand(caller); err != nil {
		return err
	}
	if c.State != ConnectorPrivate {
		return ErrInvalidTransition
	}
	c.State, c.PublishedAt = ConnectorPublished, now.UTC().Truncate(time.Millisecond)
	return nil
}

// WithdrawingAuthority is the trusted caller of a withdrawal: the platform
// operator, or an owner who must be the publisher.
type WithdrawingAuthority struct {
	Operator bool
	Owner    string
}

// Withdraw ends a Published Connector's offer for good on the command of its
// publisher or the platform operator. Repeating it changes nothing. A
// Platform Connector follows deployment configuration alone.
func (c *Connector) Withdraw(authority WithdrawingAuthority) (bool, error) {
	if c.Owner.Platform() {
		return false, ErrInvalidTransition
	}
	if !authority.Operator && !c.Owner.Is(authority.Owner) {
		return false, ErrPermissionDenied
	}
	switch c.State {
	case ConnectorWithdrawn:
		return false, nil
	case ConnectorPublished:
		c.State = ConnectorWithdrawn
		return true, nil
	default:
		return false, ErrInvalidTransition
	}
}

// Delete ends a Private Connector's existence on its owner's command and
// records PrivateConnectorDeleted; a published Connector is never deleted.
func (c *Connector) Delete(caller string, now time.Time) error {
	if err := c.ownerCommand(caller); err != nil {
		return err
	}
	if c.State != ConnectorPrivate {
		return ErrInvalidTransition
	}
	if c.events == nil {
		c.events = domainevent.NewCollection()
	}
	c.events.Add(PrivateConnectorDeleted{ConnectorID: c.ID, OwnerID: c.Owner.ID, OccurredAt: now.UTC()})
	return nil
}

// Events drains the domain events the Connector recorded since it was
// loaded; the caller dispatches them once the change is persisted.
func (c *Connector) Events() []domainevent.Event {
	if c.events == nil {
		return nil
	}
	events := c.events.Drain()
	c.events = nil
	return events
}

// ownerCommand admits change, publication and deletion only from the owner
// (Connector owner authority).
func (c *Connector) ownerCommand(caller string) error {
	if !c.Owner.Is(caller) {
		return ErrPermissionDenied
	}
	return nil
}

// bindProvider binds a remote server to the declared Provider target that
// contains its address, so the credential reaches no other host.
func bindProvider(configuration MCPConfiguration, provider ProviderDeclaration) (ProviderAccessBinding, error) {
	if !configuration.Remote() {
		return ProviderAccessBinding{}, ErrProviderConnectorNotRemote
	}
	for _, target := range provider.Targets {
		if target.Contains(configuration.URL) {
			return ProviderAccessBinding{Provider: string(provider.Name), Target: target.Name, Authentication: "credential_proxy"}, nil
		}
	}
	return ProviderAccessBinding{}, ErrCredentialTargetInapplicable
}

// validateOwnerConfiguration keeps an owner-added Connector to a remote
// server at an absolute http(s) address or a local command, neither carrying
// headers or environment; a Provider Connector is remote.
func validateOwnerConfiguration(configuration MCPConfiguration, binding *ProviderAccessBinding) error {
	if len(configuration.Headers) != 0 || len(configuration.Environment) != 0 {
		return ErrInvalidConnector
	}
	if binding != nil && !configuration.Remote() {
		return ErrProviderConnectorNotRemote
	}
	if configuration.Remote() {
		u, err := url.Parse(configuration.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return ErrInvalidConnector
		}
	}
	return configuration.Validate()
}
