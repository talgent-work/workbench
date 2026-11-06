package domain

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	connectorWorkIdentity          = "work_identity"
	connectorProviderAuthorization = "provider_authorization"
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
}

// UsesWorkIdentity reports whether the Work reaches the Connector with its
// own identity, as it reaches the Talgent Connector.
func (c SelectedConnector) UsesWorkIdentity() bool { return c.Authentication == connectorWorkIdentity }

// ProviderAuthorized reports whether a Provider credential authorizes the
// Connector's calls.
func (c SelectedConnector) ProviderAuthorized() bool {
	return c.Authentication == connectorProviderAuthorization
}

// AvailableOnManaged reports whether a Work on a Managed Computer may have
// the Connector: a Provider-authorized one is unavailable there, because its
// Provider credential stays with the platform (Managed Connector
// availability).
func (c SelectedConnector) AvailableOnManaged() bool { return !c.ProviderAuthorized() }

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
type CatalogConnector struct {
	SelectedConnector
	Offered bool
}

// ConnectorChoice is a per-Work addition or removal. It never changes a
// personal installation.
type ConnectorChoice struct {
	Name    string
	Include bool
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
	if c.Type == "http" {
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
	default:
		return ErrInvalidCapabilities
	}
	return nil
}

// selectConnectors combines Offered required entries, the starting user's
// enabled installations of Offered entries and per-Work choices. A required
// entry cannot be removed; an explicit choice of a missing or non-Offered entry
// rejects creation. Withdrawn personal defaults silently leave the selection.
func (input StartupCapabilityInput) selectConnectors() ([]SelectedConnector, error) {
	byName := make(map[string]CatalogConnector, len(input.Connectors))
	byID := make(map[string]string, len(input.Connectors))
	for _, connector := range input.Connectors {
		if _, duplicate := byName[connector.Name]; duplicate {
			return nil, fmt.Errorf("%w: ambiguous catalog Connector", ErrInvalidCapabilities)
		}
		byName[connector.Name] = connector
		byID[connector.ID] = connector.Name
	}
	selected := map[string]bool{}
	for _, id := range input.EnabledConnectors {
		if name, found := byID[id]; found && byName[name].Offered {
			selected[name] = true
		}
	}
	seen := map[string]bool{}
	for _, choice := range input.ConnectorChoices {
		connector, found := byName[choice.Name]
		if !found || seen[choice.Name] {
			return nil, fmt.Errorf("%w: invalid per-Work Connector choice", ErrInvalidCapabilities)
		}
		if choice.Include && !connector.Offered {
			return nil, fmt.Errorf("%w: Connector %s is not offered", ErrInvalidCapabilities, choice.Name)
		}
		seen[choice.Name] = true
		selected[choice.Name] = choice.Include
	}
	for _, connector := range input.Connectors {
		if connector.Required && connector.Offered {
			selected[connector.Name] = true
		}
	}
	result := make([]SelectedConnector, 0, len(selected))
	for name, include := range selected {
		if include {
			result = append(result, byName[name].SelectedConnector.Clone())
		}
	}
	slices.SortFunc(result, func(a, b SelectedConnector) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

// ConnectorOption is one Offered catalog Connector as a new Work would see it.
type ConnectorOption struct {
	Name              string
	Required          bool
	IncludedByDefault bool
	// UnavailableOnManaged says a Work on a Managed Computer leaves it out.
	UnavailableOnManaged bool
}

// ConnectorOptions lists the Offered entries a new Work may select, marking the
// default selection composed without per-Work choices.
func (input StartupCapabilityInput) ConnectorOptions() ([]ConnectorOption, error) {
	input.ConnectorChoices = nil
	defaults, err := input.selectConnectors()
	if err != nil {
		return nil, err
	}
	included := make(map[string]bool, len(defaults))
	for _, connector := range defaults {
		included[connector.Name] = true
	}
	var options []ConnectorOption
	for _, connector := range input.Connectors {
		if connector.Offered {
			options = append(options, ConnectorOption{Name: connector.Name, Required: connector.Required, IncludedByDefault: included[connector.Name], UnavailableOnManaged: !connector.AvailableOnManaged()})
		}
	}
	slices.SortFunc(options, func(a, b ConnectorOption) int { return strings.Compare(a.Name, b.Name) })
	return options, nil
}
