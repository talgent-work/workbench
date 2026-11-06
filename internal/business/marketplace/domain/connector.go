package domain

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidConnector        = errors.New("invalid MCP connector")
	ErrConnectorNotFound       = errors.New("MCP connector not found")
	ErrConnectorNotInstallable = errors.New("MCP connector cannot be personally installed")
	ErrConnectorNotOffered     = errors.New("MCP connector is not offered")
)

// ConnectorState is the catalog offer status. Withdrawn is reserved: no current
// behavior enters or leaves it, but installation and selection already refuse it.
type ConnectorState uint8

const (
	ConnectorOffered ConnectorState = iota + 1
	ConnectorWithdrawn
)

const (
	WorkIdentityAuthentication          = "work_identity"
	ProviderAuthorizationAuthentication = "provider_authorization"
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

func (c MCPConfiguration) Validate() error {
	if c.Type == "http" {
		if c.URL == "" || c.Command != "" {
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

// CatalogPresentation is what catalog browsing shows for an entry.
type CatalogPresentation struct {
	Description string `validate:"max=1024"`
	Category    string `validate:"max=64"`
}

// Connector is one platform-registered catalog MCP integration. Only platform
// publication registers or reconfigures it; users install it, never edit it.
type Connector struct {
	ProviderAccessBinding *ProviderAccessBinding
	ID                    string `validate:"required,uuid"`
	Name                  string `validate:"required,max=128"`
	Presentation          CatalogPresentation
	Required              bool
	Authentication        string
	Configuration         MCPConfiguration
	State                 ConnectorState
	Version               int
}

// EstablishRequiredPlatformConnector creates the mandatory platform integration
// authenticated by Agent-owned Work identity.
func EstablishRequiredPlatformConnector(name string, configuration MCPConfiguration, presentation CatalogPresentation) (*Connector, error) {
	c := &Connector{ID: uuid.Must(uuid.NewV7()).String(), Name: name, Presentation: presentation, Required: true,
		Configuration: configuration.Clone(), Authentication: WorkIdentityAuthentication, State: ConnectorOffered}
	return c, c.Validate()
}

// RegisterProviderConnector creates a catalog integration authenticated through
// personal Provider authorization, accepted only with evidence for exactly this
// configuration. Registration requires no personal Identity.
func RegisterProviderConnector(name string, configuration MCPConfiguration, binding ProviderAccessBinding, presentation CatalogPresentation, evidence ProviderCompatibility) (*Connector, error) {
	if err := validateProviderConfiguration(configuration, binding, evidence); err != nil {
		return nil, err
	}
	c := &Connector{ID: uuid.Must(uuid.NewV7()).String(), Name: name, Presentation: presentation,
		Configuration: configuration.Clone(), ProviderAccessBinding: &binding, Authentication: ProviderAuthorizationAuthentication, State: ConnectorOffered}
	return c, c.Validate()
}

func (c *Connector) Validate() error {
	if c == nil || domainValidator.Struct(c) != nil || !pluginSlug.MatchString(c.Name) || c.Version < 0 ||
		(c.State != ConnectorOffered && c.State != ConnectorWithdrawn) || domainValidator.Struct(c.Presentation) != nil {
		return ErrInvalidConnector
	}
	switch c.Authentication {
	case ProviderAuthorizationAuthentication:
		if c.Required || c.ProviderAccessBinding == nil {
			return ErrInvalidConnector
		}
		return validateProviderConfiguration(c.Configuration, *c.ProviderAccessBinding, ProviderCompatibility{Configuration: c.Configuration, Binding: *c.ProviderAccessBinding})
	case WorkIdentityAuthentication:
		return validateWorkIdentityConfiguration(c.Required, c.ProviderAccessBinding, c.Configuration)
	default:
		return ErrInvalidConnector
	}
}

// Reconfigure replaces the platform-owned configuration and presentation. A
// required Connector keeps Work identity authentication; a Provider Connector
// accepts target or authentication changes only with matching evidence.
// Repeating an unchanged publication reports no change.
func (c *Connector) Reconfigure(configuration MCPConfiguration, binding *ProviderAccessBinding, presentation CatalogPresentation, evidence *ProviderCompatibility) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
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

// AdmitInstallation refuses personal installation of a withdrawn or required entry.
func (c *Connector) AdmitInstallation() error {
	if c.Required || c.State != ConnectorOffered {
		return ErrConnectorNotInstallable
	}
	return nil
}

// ProvideConnection supplies a copy of the non-secret configuration for a new
// Work's selection while the entry is Offered.
func (c *Connector) ProvideConnection() (Connector, error) {
	if err := c.Validate(); err != nil {
		return Connector{}, err
	}
	if c.State != ConnectorOffered {
		return Connector{}, ErrConnectorNotOffered
	}
	result := *c
	result.Configuration = c.Configuration.Clone()
	if c.ProviderAccessBinding != nil {
		binding := *c.ProviderAccessBinding
		result.ProviderAccessBinding = &binding
	}
	return result, nil
}

// validateWorkIdentityConfiguration admits the required platform Connector:
// an HTTP MCP server with no headers or environment. The sandbox's
// Credential Proxy supplies the Work credential; the configuration holds none.
func validateWorkIdentityConfiguration(required bool, binding *ProviderAccessBinding, configuration MCPConfiguration) error {
	if !required || binding != nil || configuration.Type != "http" || len(configuration.Environment) != 0 || len(configuration.Headers) != 0 {
		return ErrInvalidConnector
	}
	return configuration.Validate()
}
