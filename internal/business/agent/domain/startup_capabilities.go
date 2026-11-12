package domain

import (
	"fmt"
	"slices"

	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// PersonalPlugin contains facts already authorized for the starting user.
// A disabled installation may still supply parameters for a required plugin.
type PersonalPlugin struct {
	Slug          string
	Enabled       bool
	Configuration pluginconfig.Configuration
}

type PluginChoice struct {
	Slug          string
	Include       bool
	Configuration pluginconfig.Configuration
}

// StartupCapabilityInput supplies creation facts, never a second retained
// definition or personal collection inside a Work.
type StartupCapabilityInput struct {
	BasePrompt          string
	RequiredPlugins     []string
	DefaultStartupAgent string
	PersonalPlugins     []PersonalPlugin
	Plugins             []PluginChoice
	Prompt              *string
	StartupAgent        *string
	// Connectors is the Marketplace catalog; EnabledConnectors identifies the
	// starting user's enabled installations; ConnectorChoices are per-Work.
	Connectors        []CatalogConnector
	EnabledConnectors []string
	ConnectorChoices  []ConnectorChoice
}

// SelectedSlugs allows application coordination to fetch only the releases
// selected by Work rules. It neither fetches releases nor installs personal items.
func (input StartupCapabilityInput) SelectedSlugs() ([]string, error) {
	selected := map[string]bool{}
	seen := map[string]bool{}
	for _, personal := range input.PersonalPlugins {
		if !pluginconfig.ValidPluginName(personal.Slug) || seen[personal.Slug] {
			return nil, fmt.Errorf("%w: invalid personal plugin facts", ErrInvalidCapabilities)
		}
		seen[personal.Slug] = true
		selected[personal.Slug] = personal.Enabled
	}
	clear(seen)
	for _, choice := range input.Plugins {
		if !pluginconfig.ValidPluginName(choice.Slug) || seen[choice.Slug] {
			return nil, fmt.Errorf("%w: invalid per-Work plugin choice", ErrInvalidCapabilities)
		}
		seen[choice.Slug] = true
		selected[choice.Slug] = choice.Include
	}
	for _, slug := range input.RequiredPlugins {
		if !pluginconfig.ValidPluginName(slug) {
			return nil, fmt.Errorf("%w: empty required plugin slug", ErrInvalidCapabilities)
		}
		selected[slug] = true
	}
	result := make([]string, 0, len(selected))
	for slug, include := range selected {
		if include {
			result = append(result, slug)
		}
	}
	slices.Sort(result)
	return result, nil
}

// NewCapabilitySnapshot is the Work capability factory. All selected plugins
// must pass static validation; an optional plugin is not silently dropped.
func NewCapabilitySnapshot(input StartupCapabilityInput, releases []PluginRelease) (CapabilitySnapshot, error) {
	slugs, err := input.SelectedSlugs()
	if err != nil {
		return CapabilitySnapshot{}, err
	}
	bySlug := make(map[string]PluginRelease, len(releases))
	for _, release := range releases {
		if _, exists := bySlug[release.Slug]; exists {
			return CapabilitySnapshot{}, fmt.Errorf("%w: ambiguous resolved release", ErrInvalidCapabilities)
		}
		bySlug[release.Slug] = release
	}
	manual := map[string]pluginconfig.Configuration{}
	for _, personal := range input.PersonalPlugins {
		manual[personal.Slug] = personal.Configuration.Clone()
	}
	for _, choice := range input.Plugins {
		if manual[choice.Slug] == nil {
			manual[choice.Slug] = pluginconfig.Configuration{}
		}
		for name, value := range choice.Configuration {
			manual[choice.Slug][name] = value
		}
	}
	snapshot := CapabilitySnapshot{Prompt: input.BasePrompt, StartupAgent: input.DefaultStartupAgent, Plugins: make([]SelectedPlugin, 0, len(slugs))}
	if snapshot.Connectors, err = input.selectConnectors(); err != nil {
		return CapabilitySnapshot{}, err
	}
	if input.Prompt != nil {
		snapshot.Prompt = *input.Prompt
	}
	if input.StartupAgent != nil {
		snapshot.StartupAgent = *input.StartupAgent
	}
	for _, slug := range slugs {
		release, found := bySlug[slug]
		if !found {
			return CapabilitySnapshot{}, fmt.Errorf("%w: no resolved release for plugin %s", ErrInvalidCapabilities, slug)
		}
		values, err := manual[slug].Effective(release.Parameters)
		if err != nil {
			return CapabilitySnapshot{}, fmt.Errorf("plugin %s: %w", slug, err)
		}
		snapshot.Plugins = append(snapshot.Plugins, SelectedPlugin{Release: release.Clone(), Configuration: values})
	}
	if err := snapshot.Validate(); err != nil {
		return CapabilitySnapshot{}, err
	}
	if snapshot.StartupAgentDropped() {
		return CapabilitySnapshot{}, fmt.Errorf("%w: startup Agent is not declared by the selected plugins", ErrInvalidCapabilities)
	}
	return snapshot, nil
}
