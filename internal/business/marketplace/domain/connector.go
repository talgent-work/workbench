package domain

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/google/uuid"
)

var (
	ErrInvalidConnector        = errors.New("invalid MCP connector")
	ErrConnectorNotFound       = errors.New("MCP connector not found")
	ErrConnectorNotInstallable = errors.New("MCP connector cannot be personally installed")
)

// ConnectorState is the Connector's Lifecycle State: an owner's Connector is
// added Private, published to every owner and withdrawn for good. A Platform
// Connector is established Published.
type ConnectorState uint8

const (
	ConnectorPrivate ConnectorState = iota + 1
	ConnectorPublished
	ConnectorWithdrawn
)

const (
	WorkIdentityAuthentication          = "work_identity"
	ProviderAuthorizationAuthentication = "provider_authorization"
	// NoAuthentication is a credential-free Connector's: it receives no
	// credential (Credential-free Connector).
	NoAuthentication = "none"
)

// MCPConfiguration retains a native stdio or HTTP declaration without issued credentials.
type MCPConfiguration struct {
	Type        string
	URL         string
	Environment map[string]string
	Headers     map[string]string
	Command     string   `validate:"max=255"`
	Arguments   []string `validate:"max=32,dive,max=4096"`
}

func (c MCPConfiguration) Clone() MCPConfiguration {
	c.Arguments = slices.Clone(c.Arguments)
	c.Environment = maps.Clone(c.Environment)
	c.Headers = maps.Clone(c.Headers)
	return c
}

// Remote reports whether Works reach the server over HTTP.
func (c MCPConfiguration) Remote() bool { return c.Type == "http" }

func (c MCPConfiguration) Validate() error {
	if c.Type == "http" {
		if c.URL == "" || c.Command != "" || len(c.URL) > 2048 {
			return ErrInvalidConnector
		}
		return nil
	}
	if c.Type != "" && c.Type != "stdio" {
		return ErrInvalidConnector
	}
	if domainValidator.Struct(c) != nil || strings.TrimSpace(c.Command) == "" || c.URL != "" || strings.ContainsRune(c.Command, 0) {
		return ErrInvalidConnector
	}
	for _, argument := range c.Arguments {
		if strings.ContainsRune(argument, 0) {
			return ErrInvalidConnector
		}
	}
	return nil
}

// CatalogPresentation is what catalog browsing shows for an entry: its
// description, category and optional icon image.
type CatalogPresentation struct {
	Description string `validate:"max=1024"`
	Category    string `validate:"max=64"`
	Icon        *ConnectorIcon
}

func (p CatalogPresentation) validate() error {
	if domainValidator.Struct(p) != nil {
		return ErrInvalidConnector
	}
	if p.Icon != nil {
		return p.Icon.Validate()
	}
	return nil
}

// Connector is one MCP integration that the deployment provides or an owner
// adds. Only its owner sees it until its owner publishes it to every owner.
type Connector struct {
	ProviderAccessBinding *ProviderAccessBinding
	Owner                 ConnectorOwner
	ID                    string `validate:"required,uuid"`
	Name                  string `validate:"required,max=128"`
	Presentation          CatalogPresentation
	Required              bool
	Authentication        string
	Configuration         MCPConfiguration
	State                 ConnectorState
	// PublishedAt is when the Connector was published; zero while Private.
	PublishedAt time.Time
	Version     int
	events      domainevent.Collection
}

// EstablishRequiredPlatformConnector creates the mandatory platform integration
// authenticated by Agent-owned Work identity.
func EstablishRequiredPlatformConnector(name string, configuration MCPConfiguration, presentation CatalogPresentation) (*Connector, error) {
	c := &Connector{ID: uuid.Must(uuid.NewV7()).String(), Owner: PlatformOwner, Name: name, Presentation: presentation, Required: true,
		Configuration: configuration.Clone(), Authentication: WorkIdentityAuthentication, State: ConnectorPublished}
	return c, c.Validate()
}

// EstablishPlatformProviderConnector creates a Platform Connector authenticated
// through personal Provider authorization, accepted only with evidence for
// exactly this configuration. It requires no personal Identity.
func EstablishPlatformProviderConnector(name string, configuration MCPConfiguration, binding ProviderAccessBinding, presentation CatalogPresentation, evidence ProviderCompatibility) (*Connector, error) {
	if err := validateProviderConfiguration(configuration, binding, evidence); err != nil {
		return nil, err
	}
	c := &Connector{ID: uuid.Must(uuid.NewV7()).String(), Owner: PlatformOwner, Name: name, Presentation: presentation,
		Configuration: configuration.Clone(), ProviderAccessBinding: &binding, Authentication: ProviderAuthorizationAuthentication, State: ConnectorPublished}
	return c, c.Validate()
}

func (c *Connector) Validate() error {
	if c == nil || domainValidator.Struct(c) != nil || !pluginSlug.MatchString(c.Name) || c.Version < 0 || c.Owner.validate() != nil ||
		c.State < ConnectorPrivate || c.State > ConnectorWithdrawn {
		return ErrInvalidConnector
	}
	if err := c.Presentation.validate(); err != nil {
		return err
	}
	if c.Owner.Platform() && c.State == ConnectorPrivate {
		return ErrInvalidConnector
	}
	if !c.Owner.Platform() {
		if err := validateOwnerConfiguration(c.Configuration, c.ProviderAccessBinding); err != nil {
			return err
		}
	}
	switch c.Authentication {
	case ProviderAuthorizationAuthentication:
		if c.Required || c.ProviderAccessBinding == nil {
			return ErrInvalidConnector
		}
		return validateProviderBinding(c.Configuration, *c.ProviderAccessBinding)
	case WorkIdentityAuthentication:
		if !c.Owner.Platform() {
			return ErrInvalidConnector
		}
		return validateWorkIdentityConfiguration(c.Required, c.ProviderAccessBinding, c.Configuration)
	case NoAuthentication:
		if c.Required || c.ProviderAccessBinding != nil || c.Owner.Platform() {
			return ErrInvalidConnector
		}
		return c.Configuration.Validate()
	default:
		return ErrInvalidConnector
	}
}

// Reconfigure replaces a Platform Connector's deployment-owned configuration
// and presentation. A required Connector keeps Work identity authentication;
// a Provider Connector accepts target or authentication changes only with
// matching evidence. Repeating an unchanged publication reports no change.
func (c *Connector) Reconfigure(configuration MCPConfiguration, binding *ProviderAccessBinding, presentation CatalogPresentation, evidence *ProviderCompatibility) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	if !c.Owner.Platform() {
		return false, ErrInvalidConnector
	}
	candidate := *c
	candidate.Configuration = configuration.Clone()
	candidate.Presentation = presentation
	candidate.ProviderAccessBinding = nil
	if binding != nil {
		copied := *binding
		candidate.ProviderAccessBinding = &copied
	}
	if c.Authentication == ProviderAuthorizationAuthentication {
		if binding == nil || evidence == nil {
			return false, ErrInvalidConnector
		}
		if err := validateProviderConfiguration(configuration, *binding, *evidence); err != nil {
			return false, err
		}
	}
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	if reflect.DeepEqual(&candidate, c) {
		return false, nil
	}
	*c = candidate
	return true, nil
}

// InCatalogOf reports whether the Connector is in an owner's Connector
// catalog: a Platform or Published Connector, or the owner's own Private
// one. A Withdrawn Connector is in no catalog (Catalog withdrawal).
func (c *Connector) InCatalogOf(owner string) bool {
	switch c.State {
	case ConnectorPublished:
		return true
	case ConnectorPrivate:
		return c.Owner.Is(owner)
	default:
		return false
	}
}

// AdmitInstallation admits an owner's installation only while the Connector
// is in that owner's Connector catalog and neither Withdrawn nor required.
func (c *Connector) AdmitInstallation(owner string) error {
	if c.Required || !c.InCatalogOf(owner) {
		return ErrConnectorNotInstallable
	}
	return nil
}

// validateWorkIdentityConfiguration admits the required platform Connector:
// an HTTP MCP server with no headers or environment. The sandbox's
// Credential Proxy supplies the Work's Identity Token; the configuration
// holds none.
func validateWorkIdentityConfiguration(required bool, binding *ProviderAccessBinding, configuration MCPConfiguration) error {
	if !required || binding != nil || configuration.Type != "http" || len(configuration.Environment) != 0 || len(configuration.Headers) != 0 {
		return ErrInvalidConnector
	}
	return configuration.Validate()
}
