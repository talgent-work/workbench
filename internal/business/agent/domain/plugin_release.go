package domain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// PluginRelease is an Agent-owned copy of immutable upstream catalog facts.
// No catalog lookup or individual component upgrade occurs after capture.
type PluginRelease struct {
	Slug         string
	Source       string
	Version      string
	RetrievalRef string
	NativeEntry  []byte
	Skills       []SkillComponent
	Agents       []AgentComponent
	MCPServers   []MCPComponent
	Parameters   pluginconfig.ParameterDeclarations
}

// Components are declarations only; package file contents are never retained.
type SkillComponent struct{ Name, Description, ArgumentHint string }
type AgentComponent struct{ Name, Description string }
type ConnectorProviderRelationship struct{ ConnectorID, Provider, Target, Authentication, Status, Reason string }

type MCPComponent struct {
	Relationship ConnectorProviderRelationship
	Name         string
	Definition   []byte
}

func (r PluginRelease) Validate() error {
	if !pluginconfig.ValidPluginName(r.Slug) {
		return fmt.Errorf("%w: invalid plugin slug", ErrInvalidCapabilities)
	}
	for _, value := range []string{r.Slug, r.Source, r.Version, r.RetrievalRef} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: incomplete plugin release", ErrInvalidCapabilities)
		}
	}
	if len(r.NativeEntry) == 0 {
		return fmt.Errorf("%w: missing native plugin entry", ErrInvalidCapabilities)
	}
	if err := r.Parameters.Validate(); err != nil {
		return fmt.Errorf("plugin %s: %w", r.Slug, err)
	}
	for kind, names := range [][]string{
		componentNames(r.Skills, func(c SkillComponent) string { return c.Name }),
		componentNames(r.Agents, func(c AgentComponent) string { return c.Name }),
		componentNames(r.MCPServers, func(c MCPComponent) string { return c.Name }),
	} {
		seen := map[string]bool{}
		for _, name := range names {
			if strings.TrimSpace(name) == "" || seen[name] || (kind < 2 && !strings.HasPrefix(name, r.Slug+":")) {
				return fmt.Errorf("%w: invalid plugin component name", ErrInvalidCapabilities)
			}
			seen[name] = true
		}
	}
	return nil
}

func componentNames[T any](components []T, name func(T) string) []string {
	names := make([]string, len(components))
	for i, component := range components {
		names[i] = name(component)
	}
	return names
}

func (r PluginRelease) Clone() PluginRelease {
	r.NativeEntry = slices.Clone(r.NativeEntry)
	r.Skills = slices.Clone(r.Skills)
	r.Agents = slices.Clone(r.Agents)
	r.MCPServers = slices.Clone(r.MCPServers)
	for i := range r.MCPServers {
		r.MCPServers[i].Definition = slices.Clone(r.MCPServers[i].Definition)
	}
	r.Parameters = r.Parameters.Clone()
	return r
}
