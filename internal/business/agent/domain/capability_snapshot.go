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
	// ApplicationEndpoints are the MCP endpoints the Work's Application gave
	// it at creation, with their fixed headers.
	ApplicationEndpoints []ApplicationMCPEndpoint
	// Sandbox records that a Work on a Managed Computer runs in Claude
	// Code's sandbox, chosen at its first start and never turned off.
	Sandbox bool
}

func (c CapabilitySnapshot) Clone() CapabilitySnapshot {
	c.Connectors = slices.Clone(c.Connectors)
	for i := range c.Connectors {
		c.Connectors[i] = c.Connectors[i].Clone()
	}
	c.ApplicationEndpoints = cloneApplicationEndpoints(c.ApplicationEndpoints)
	c.Plugins = slices.Clone(c.Plugins)
	for i := range c.Plugins {
		c.Plugins[i].Release = c.Plugins[i].Release.Clone()
		c.Plugins[i].Configuration = c.Plugins[i].Configuration.Clone()
	}
	return c
}

// Validate checks a retained snapshot. Its startup Agent stays the Work's
// selection even when no selected release declares it any more (Dropped
// startup Agent); creation alone requires a declared one.
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
	if err := validateApplicationEndpoints(c.ApplicationEndpoints, c.Connectors); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, plugin := range c.Plugins {
		if err := plugin.Release.Validate(); err != nil {
			return err
		}
		if seen[plugin.Release.Slug] {
			return fmt.Errorf("%w: duplicate plugin slug", ErrInvalidCapabilities)
		}
		seen[plugin.Release.Slug] = true
		// One local marketplace serves a Work's Plugins under its catalog's name.
		if plugin.Release.CatalogName != c.PluginCatalog() {
			return fmt.Errorf("%w: plugins from different catalogs", ErrInvalidCapabilities)
		}
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
	}
	return nil
}

// PluginCatalog names the catalog whose marketplace serves the Work's
// selected Plugins; empty when it selects none.
func (c CapabilitySnapshot) PluginCatalog() string {
	if len(c.Plugins) == 0 {
		return ""
	}
	return c.Plugins[0].Release.CatalogName
}

// LaunchedStartupAgent is the startup Agent a start launches with: the
// Work's selection while a selected release declares it, and none once the
// releases the Work took dropped it (Dropped startup Agent).
func (c CapabilitySnapshot) LaunchedStartupAgent() string {
	if c.StartupAgent == "" || c.declaresStartupAgent() {
		return c.StartupAgent
	}
	return ""
}

// declaresStartupAgent reports whether a selected release declares the
// Work's startup Agent.
func (c CapabilitySnapshot) declaresStartupAgent() bool {
	return slices.ContainsFunc(c.Plugins, func(plugin SelectedPlugin) bool { return plugin.Release.declaresAgent(c.StartupAgent) })
}

// StartupAgentDropped reports whether the Work selected a startup Agent that
// no release it took declares any more.
func (c CapabilitySnapshot) StartupAgentDropped() bool {
	return c.StartupAgent != "" && c.LaunchedStartupAgent() == ""
}

// UpgradePlugins takes the current release declarations of every selected
// Plugin (Start upgrade); the selection, Prompt, startup Agent and Connectors
// stay as established. A Plugin without a current release keeps the
// declarations the Work last took (Unresolvable release), under the catalog
// the current releases name, so one marketplace still serves every Plugin of
// the start. A release that no
// longer declares the startup Agent is still taken: the start runs without
// one (Dropped startup Agent). A retained value that differs from the old
// release's default was chosen by the starting user and carries over while
// the new release still accepts it; any other parameter takes the new
// release's default (Parameter carry-over).
func (c CapabilitySnapshot) UpgradePlugins(current []PluginRelease) (CapabilitySnapshot, error) {
	bySlug := make(map[string]PluginRelease, len(current))
	for _, release := range current {
		bySlug[release.Slug] = release
	}
	upgraded := c.Clone()
	for i, selected := range upgraded.Plugins {
		release, found := bySlug[selected.Release.Slug]
		if !found {
			if len(current) > 0 {
				upgraded.Plugins[i].Release.CatalogName = current[0].CatalogName
			}
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
