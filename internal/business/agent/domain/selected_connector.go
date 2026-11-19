package domain

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	connectorWorkIdentity          = "work_identity"
	connectorProviderAuthorization = "provider_authorization"
	// connectorNoCredential is a Connector that declares no Provider and
	// receives no credential (Credential-free Connector).
	connectorNoCredential = "none"

	// connectorRemote is the native transport of a remote MCP server; a
	// local command's is stdio or empty.
	connectorRemote = "http"
)

// SelectedConnector is a Connector retained by a Work: its catalog identity and
// native non-secret configuration. Work identity and Provider credentials are
// supplied only at runtime and never retained here.
type SelectedConnector struct {
	ID             string `validate:"required,uuid"`
	Name           string `validate:"required,max=128"`
	Required       bool
	Authentication string
	Type           string
	URL            string
	Command        string   `validate:"max=255"`
	Arguments      []string `validate:"max=32,dive,max=4096"`
	Environment    map[string]string
	Headers        map[string]string
	// Provider, Target and ProviderAuthentication are Marketplace-validated
	// requirements, present only for Provider-authenticated Connectors.
	Provider, Target, ProviderAuthentication string
	// TargetCredentialsExpire is whether the Provider declares that its
	// target credentials expire, as Marketplace's Connector Provider
	// Relationship states it.
	TargetCredentialsExpire bool
}

// UsesWorkIdentity reports whether the Work reaches the Connector with its
// own identity, as it reaches the Talgent Connector.
func (c SelectedConnector) UsesWorkIdentity() bool { return c.Authentication == connectorWorkIdentity }

// ProviderAuthorized reports whether a Provider credential authorizes the
// Connector's calls.
func (c SelectedConnector) ProviderAuthorized() bool {
	return c.Authentication == connectorProviderAuthorization
}

// AvailableOnManaged reports whether a Work on a Managed Computer keeps the
// Connector (Managed Connector availability). A Provider Connector whose
// target credential declares no expiry is left out (Unavailable Provider
// Connectors): only a short-lived target credential may reach the machine.
// So is a Provider Connector that runs as a local command, since the local
// proxy adds a target credential only at a remote Connector's explicit
// address and the credential never enters a Work's process.
func (c SelectedConnector) AvailableOnManaged() bool {
	return !c.ProviderAuthorized() || (c.TargetCredentialsExpire && c.Type == connectorRemote)
}

// ManagedProviderConnectors are the Provider Connectors a Work on a Managed
// Computer keeps: only their target credentials reach its Computer (Managed
// Provider credentials).
func (c CapabilitySnapshot) ManagedProviderConnectors() []SelectedConnector {
	var connectors []SelectedConnector
	for _, connector := range c.Connectors {
		if connector.ProviderAuthorized() && connector.AvailableOnManaged() {
			connectors = append(connectors, connector)
		}
	}
	return connectors
}

// AvailableOn is the snapshot a Work retains on its Computer: on a Managed
// Computer, without the Connectors unavailable there, which does not reject
// creation.
func (c CapabilitySnapshot) AvailableOn(managed bool) CapabilitySnapshot {
	if managed {
		c.Connectors = slices.DeleteFunc(slices.Clone(c.Connectors), func(connector SelectedConnector) bool { return !connector.AvailableOnManaged() })
	}
	return c
}

// CatalogConnector is a Marketplace catalog fact supplied for startup
// composition; only Offered entries may be selected for a new Work.
// Publisher names who published it, empty for a Platform Connector.
type CatalogConnector struct {
	SelectedConnector
	Offered   bool
	Publisher string
}

// ConnectorChoice is a per-Work addition or removal of a Connector by its
// identity. It never changes a personal installation.
type ConnectorChoice struct {
	ConnectorID string
	Include     bool
}

func (c SelectedConnector) Clone() SelectedConnector {
	c.Arguments = slices.Clone(c.Arguments)
	c.Environment = maps.Clone(c.Environment)
	c.Headers = maps.Clone(c.Headers)
	return c
}

func (c SelectedConnector) Validate() error {
	if validation.Struct(c) != nil || strings.ContainsRune(c.Command, 0) {
		return ErrInvalidCapabilities
	}
	for _, argument := range c.Arguments {
		if strings.ContainsRune(argument, 0) {
			return ErrInvalidCapabilities
		}
	}
	if c.Type == connectorRemote {
		if c.URL == "" || c.Command != "" {
			return ErrInvalidCapabilities
		}
	} else if (c.Type != "" && c.Type != "stdio") || strings.TrimSpace(c.Command) == "" || c.URL != "" {
		return ErrInvalidCapabilities
	}
	switch c.Authentication {
	case connectorWorkIdentity:
		if !c.Required || c.Provider != "" {
			return ErrInvalidCapabilities
		}
	case connectorProviderAuthorization:
		if c.Required || c.Provider == "" || c.Target == "" || c.ProviderAuthentication == "" {
			return ErrInvalidCapabilities
		}
	case connectorNoCredential:
		if c.Required || c.Provider != "" || c.Target != "" || c.ProviderAuthentication != "" {
			return ErrInvalidCapabilities
		}
	default:
		return ErrInvalidCapabilities
	}
	return nil
}

// ErrDuplicateConnectorName refuses a Work whose selected Connectors share a
// native name, which names one MCP server in the Work.
var ErrDuplicateConnectorName = fmt.Errorf("%w: two selected Connectors share a name", ErrInvalidCapabilities)

// selectConnectors combines Offered required entries, the starting user's
// enabled installations of Offered entries and per-Work choices, each by
// Connector identity. A required entry cannot be removed; an explicit choice
// of a missing or non-Offered entry rejects creation, and so do two selected
// Connectors of one name. Withdrawn personal defaults silently leave the
// selection.
func (input StartupCapabilityInput) selectConnectors() ([]SelectedConnector, error) {
	byID := make(map[string]CatalogConnector, len(input.Connectors))
	for _, connector := range input.Connectors {
		if _, duplicate := byID[connector.ID]; duplicate {
			return nil, fmt.Errorf("%w: ambiguous catalog Connector", ErrInvalidCapabilities)
		}
		byID[connector.ID] = connector
	}
	selected := map[string]bool{}
	for _, id := range input.EnabledConnectors {
		if connector, found := byID[id]; found && connector.Offered {
			selected[id] = true
		}
	}
	seen := map[string]bool{}
	for _, choice := range input.ConnectorChoices {
		connector, found := byID[choice.ConnectorID]
		if !found || seen[choice.ConnectorID] {
			return nil, fmt.Errorf("%w: invalid per-Work Connector choice", ErrInvalidCapabilities)
		}
		if choice.Include && !connector.Offered {
			return nil, fmt.Errorf("%w: Connector %s is not offered", ErrInvalidCapabilities, connector.Name)
		}
		seen[choice.ConnectorID] = true
		selected[choice.ConnectorID] = choice.Include
	}
	for _, connector := range input.Connectors {
		if connector.Required && connector.Offered {
			selected[connector.ID] = true
		}
	}
	result := make([]SelectedConnector, 0, len(selected))
	names := map[string]bool{}
	for id, include := range selected {
		if !include {
			continue
		}
		connector := byID[id]
		if names[connector.Name] {
			return nil, fmt.Errorf("%w: %s; choose one of them for this Work", ErrDuplicateConnectorName, connector.Name)
		}
		names[connector.Name] = true
		result = append(result, connector.SelectedConnector.Clone())
	}
	slices.SortFunc(result, func(a, b SelectedConnector) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

// ConnectorOption is one Offered catalog Connector as a new Work would see it.
type ConnectorOption struct {
	ID, Name string
	// Publisher names who published it, empty for a Platform Connector.
	Publisher         string
	Required          bool
	IncludedByDefault bool
	// UnavailableOnManaged says a Work on a Managed Computer leaves it out.
	UnavailableOnManaged bool
}

// ConnectorOptions lists the Offered entries a new Work may select, marking
// the default selection composed without per-Work choices. Defaults that
// share a name stay marked, so the person removes all but one of them.
func (input StartupCapabilityInput) ConnectorOptions() []ConnectorOption {
	included := map[string]bool{}
	for _, id := range input.EnabledConnectors {
		included[id] = true
	}
	var options []ConnectorOption
	for _, connector := range input.Connectors {
		if connector.Offered {
			options = append(options, ConnectorOption{ID: connector.ID, Name: connector.Name, Publisher: connector.Publisher, Required: connector.Required,
				IncludedByDefault: connector.Required || included[connector.ID], UnavailableOnManaged: !connector.AvailableOnManaged()})
		}
	}
	slices.SortFunc(options, func(a, b ConnectorOption) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Publisher, b.Publisher))
	})
	return options
}
