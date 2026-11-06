package domain

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// SelectedPlugin retains the complete release and effective typed parameters.
// Requiredness and personal enablement are input facts, not duplicated state.
type SelectedPlugin struct {
	Release       PluginRelease
	Configuration pluginconfig.Configuration
}

type CapabilitySnapshot struct {
	Prompt       string
	StartupAgent string
	Plugins      []SelectedPlugin
	Connectors   []SelectedConnector
	// Sandbox records that a Work on a Managed Computer runs in Claude
	// Code's sandbox, chosen at its first start and never turned off.
	Sandbox bool
}

func (c CapabilitySnapshot) Clone() CapabilitySnapshot {
	c.Connectors = slices.Clone(c.Connectors)
	for i := range c.Connectors {
		c.Connectors[i] = c.Connectors[i].Clone()
	}
	c.Plugins = slices.Clone(c.Plugins)
	for i := range c.Plugins {
		c.Plugins[i].Release = c.Plugins[i].Release.Clone()
		c.Plugins[i].Configuration = c.Plugins[i].Configuration.Clone()
	}
	return c
}

func (c CapabilitySnapshot) Validate() error {
	connectors := map[string]bool{}
	for _, connector := range c.Connectors {
		if err := connector.Validate(); err != nil {
			return err
		}
		if connectors[connector.Name] {
			return fmt.Errorf("%w: duplicate Connector", ErrInvalidCapabilities)
		}
		connectors[connector.Name] = true
	}
	seen := map[string]bool{}
	roleFound := c.StartupAgent == ""
	for _, plugin := range c.Plugins {
		if err := plugin.Release.Validate(); err != nil {
			return err
		}
		if seen[plugin.Release.Slug] {
			return fmt.Errorf("%w: duplicate plugin slug", ErrInvalidCapabilities)
		}
		seen[plugin.Release.Slug] = true
		effective, err := plugin.Configuration.Effective(plugin.Release.Parameters)
		if err != nil {
			return fmt.Errorf("plugin %s: %w", plugin.Release.Slug, err)
		}
		// Retained snapshots must already be complete: neither a new default nor
		// a removed parameter may be silently interpreted during reconstitution.
		if len(effective) != len(plugin.Configuration) {
			return fmt.Errorf("%w: incomplete effective parameters", ErrInvalidCapabilities)
		}
		for name, value := range effective {
			if !reflect.DeepEqual(value, plugin.Configuration[name]) {
				return fmt.Errorf("%w: inconsistent effective parameters", ErrInvalidCapabilities)
			}
		}
		for _, agent := range plugin.Release.Agents {
			if agent.Name == c.StartupAgent {
				roleFound = true
			}
		}
	}
	if !roleFound {
		return fmt.Errorf("%w: startup Agent is not declared by the selected plugins", ErrInvalidCapabilities)
	}
	return nil
}

// UpgradePlugins takes up the current release of every selected Plugin; the
// selection, Prompt, startup Agent and Connectors stay as established. A
// Plugin without a current release keeps its retained one, as Plugin
// preparation degrades rather than blocks a start. A retained value that
// differs from the old release's default was chosen by the starting user and
// carries over while the new release still accepts it; any other parameter
// takes the new release's default.
func (c CapabilitySnapshot) UpgradePlugins(current []PluginRelease) (CapabilitySnapshot, error) {
	bySlug := make(map[string]PluginRelease, len(current))
	for _, release := range current {
		bySlug[release.Slug] = release
	}
	upgraded := c.Clone()
	for i, selected := range upgraded.Plugins {
		release, found := bySlug[selected.Release.Slug]
		if !found {
			continue
		}
		chosen := pluginconfig.Configuration{}
		for name, declaration := range release.Parameters {
			value, ok := selected.Configuration[name]
			if !ok || declaration.ValidateValue(value) != nil {
				continue
			}
			if previous, declared := selected.Release.Parameters[name]; declared && reflect.DeepEqual(value, previous.Default) {
				continue
			}
			chosen[name] = value
		}
		values, err := chosen.Effective(release.Parameters)
		if err != nil {
			return CapabilitySnapshot{}, fmt.Errorf("plugin %s: %w", release.Slug, err)
		}
		upgraded.Plugins[i] = SelectedPlugin{Release: release.Clone(), Configuration: values}
	}
	if err := upgraded.Validate(); err != nil {
		return CapabilitySnapshot{}, err
	}
	return upgraded, nil
}
