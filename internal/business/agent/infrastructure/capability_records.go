package infrastructure

import (
	"encoding/json"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

type definitionCapabilitiesRecord struct {
	Prompt          string   `json:"prompt"`
	StartupAgent    string   `json:"startup_agent"`
	RequiredPlugins []string `json:"required_plugins"`
}

type snapshotRecord struct {
	Prompt       string                 `json:"prompt"`
	StartupAgent string                 `json:"startup_agent"`
	Plugins      []selectedPluginRecord `json:"plugins"`
	Connectors   []connectorRecord      `json:"connectors"`
	// ApplicationEndpoints keeps the headers the Application gave; they
	// leave storage only for the Work's own launch.
	ApplicationEndpoints []applicationEndpointRecord `json:"application_endpoints,omitempty"`
	Sandbox              bool                        `json:"sandbox,omitempty"`
}

type applicationEndpointRecord struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type connectorRecord struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	Required               bool              `json:"required"`
	Command                string            `json:"command"`
	Arguments              []string          `json:"args"`
	Authentication         string            `json:"authentication"`
	Type                   string            `json:"type,omitempty"`
	URL                    string            `json:"url,omitempty"`
	Environment            map[string]string `json:"env,omitempty"`
	Headers                map[string]string `json:"headers,omitempty"`
	Provider               string            `json:"provider,omitempty"`
	Target                 string            `json:"target,omitempty"`
	ProviderAuthentication string            `json:"provider_authentication,omitempty"`
}

type selectedPluginRecord struct {
	Release       releaseRecord  `json:"release"`
	Configuration map[string]any `json:"configuration"`
}

type releaseRecord struct {
	CatalogName  string                             `json:"catalog_name"`
	Slug         string                             `json:"slug"`
	Source       string                             `json:"source"`
	Version      string                             `json:"version"`
	RetrievalRef string                             `json:"retrieval_ref"`
	NativeEntry  []byte                             `json:"native_entry"`
	Skills       []skillRecord                      `json:"skills"`
	Agents       []agentComponentRecord             `json:"agents"`
	MCPServers   []mcpRecord                        `json:"mcp_servers"`
	Parameters   pluginconfig.ParameterDeclarations `json:"parameters"`
}

type skillRecord struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argument_hint"`
}

type agentComponentRecord struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type mcpRecord struct {
	Relationship domain.ConnectorProviderRelationship `json:"relationship"`
	Name         string                               `json:"name"`
	Definition   []byte                               `json:"definition"`
}

func encodeCapabilities(value domain.CapabilityConfiguration) (string, error) {
	return encode(definitionCapabilitiesRecord{Prompt: value.Prompt, StartupAgent: value.StartupAgent, RequiredPlugins: value.RequiredPlugins})
}

func decodeCapabilities(body string) (domain.CapabilityConfiguration, error) {
	var record definitionCapabilitiesRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return domain.CapabilityConfiguration{}, err
	}
	value := domain.CapabilityConfiguration{Prompt: record.Prompt, StartupAgent: record.StartupAgent, RequiredPlugins: record.RequiredPlugins}
	return value, value.Validate()
}

func encodeCapabilitySnapshot(value domain.CapabilitySnapshot) (string, error) {
	// Preserve nil versus empty collections, so a storage round trip leaves
	// the retained capabilities exactly as they were admitted.
	record := snapshotRecord{Prompt: value.Prompt, StartupAgent: value.StartupAgent, Sandbox: value.Sandbox}
	if value.Connectors != nil {
		record.Connectors = make([]connectorRecord, 0, len(value.Connectors))
	}
	for _, c := range value.Connectors {
		record.Connectors = append(record.Connectors, connectorRecord{ID: c.ID, Name: c.Name, Required: c.Required, Command: c.Command, Arguments: c.Arguments, Authentication: c.Authentication,
			Type: c.Type, URL: c.URL, Environment: c.Environment, Headers: c.Headers, Provider: c.Provider, Target: c.Target, ProviderAuthentication: c.ProviderAuthentication})
	}
	for _, endpoint := range value.ApplicationEndpoints {
		record.ApplicationEndpoints = append(record.ApplicationEndpoints, applicationEndpointRecord{Name: endpoint.Name, URL: endpoint.URL, Headers: endpoint.Headers})
	}
	if value.Plugins != nil {
		record.Plugins = make([]selectedPluginRecord, 0, len(value.Plugins))
	}
	for _, plugin := range value.Plugins {
		r := plugin.Release
		release := releaseRecord{CatalogName: r.CatalogName, Slug: r.Slug, Source: r.Source, Version: r.Version, RetrievalRef: r.RetrievalRef, NativeEntry: r.NativeEntry, Parameters: r.Parameters}
		if r.Skills != nil {
			release.Skills = make([]skillRecord, 0, len(r.Skills))
		}
		if r.Agents != nil {
			release.Agents = make([]agentComponentRecord, 0, len(r.Agents))
		}
		if r.MCPServers != nil {
			release.MCPServers = make([]mcpRecord, 0, len(r.MCPServers))
		}
		for _, c := range r.Skills {
			release.Skills = append(release.Skills, skillRecord{c.Name, c.Description, c.ArgumentHint})
		}
		for _, c := range r.Agents {
			release.Agents = append(release.Agents, agentComponentRecord{c.Name, c.Description})
		}
		for _, c := range r.MCPServers {
			release.MCPServers = append(release.MCPServers, mcpRecord{Name: c.Name, Definition: c.Definition, Relationship: c.Relationship})
		}
		record.Plugins = append(record.Plugins, selectedPluginRecord{Release: release, Configuration: plugin.Configuration})
	}
	return encode(record)
}

func decodeCapabilitySnapshot(body string) (domain.CapabilitySnapshot, error) {
	var record snapshotRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return domain.CapabilitySnapshot{}, err
	}
	value := domain.CapabilitySnapshot{Prompt: record.Prompt, StartupAgent: record.StartupAgent, Sandbox: record.Sandbox}
	if record.Connectors != nil {
		value.Connectors = make([]domain.SelectedConnector, 0, len(record.Connectors))
	}
	for _, c := range record.Connectors {
		value.Connectors = append(value.Connectors, domain.SelectedConnector{ID: c.ID, Name: c.Name, Required: c.Required, Command: c.Command, Arguments: c.Arguments, Authentication: c.Authentication,
			Type: c.Type, URL: c.URL, Environment: c.Environment, Headers: c.Headers, Provider: c.Provider, Target: c.Target, ProviderAuthentication: c.ProviderAuthentication})
	}
	for _, endpoint := range record.ApplicationEndpoints {
		value.ApplicationEndpoints = append(value.ApplicationEndpoints, domain.ApplicationMCPEndpoint{Name: endpoint.Name, URL: endpoint.URL, Headers: endpoint.Headers})
	}
	if record.Plugins != nil {
		value.Plugins = make([]domain.SelectedPlugin, 0, len(record.Plugins))
	}
	for _, plugin := range record.Plugins {
		r := plugin.Release
		release := domain.PluginRelease{CatalogName: r.CatalogName, Slug: r.Slug, Source: r.Source, Version: r.Version, RetrievalRef: r.RetrievalRef, NativeEntry: r.NativeEntry, Parameters: r.Parameters}
		if r.Skills != nil {
			release.Skills = make([]domain.SkillComponent, 0, len(r.Skills))
		}
		if r.Agents != nil {
			release.Agents = make([]domain.AgentComponent, 0, len(r.Agents))
		}
		if r.MCPServers != nil {
			release.MCPServers = make([]domain.MCPComponent, 0, len(r.MCPServers))
		}
		for name, d := range release.Parameters {
			d.Default = pluginconfig.JSONValue(d.Default)
			release.Parameters[name] = d
		}
		for _, c := range r.Skills {
			release.Skills = append(release.Skills, domain.SkillComponent{Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint})
		}
		for _, c := range r.Agents {
			release.Agents = append(release.Agents, domain.AgentComponent{Name: c.Name, Description: c.Description})
		}
		for _, c := range r.MCPServers {
			release.MCPServers = append(release.MCPServers, domain.MCPComponent{Name: c.Name, Definition: c.Definition, Relationship: c.Relationship})
		}
		value.Plugins = append(value.Plugins, domain.SelectedPlugin{Release: release, Configuration: pluginconfig.FromJSON(plugin.Configuration)})
	}
	return value, value.Validate()
}
