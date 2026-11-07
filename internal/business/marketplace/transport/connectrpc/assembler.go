package connectrpc

import (
	"errors"

	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"google.golang.org/protobuf/types/known/structpb"
)

func installationMessage(plugin query.InstalledPlugin) (*marketplacepublicv1.InstalledPlugin, error) {
	values := make(map[string]any, len(plugin.Configuration))
	for key, value := range plugin.Configuration {
		values[key] = wireValue(value)
	}
	configuration, err := structpb.NewStruct(values)
	if err != nil {
		return nil, errors.New("marketplace: cannot encode personal configuration")
	}
	return &marketplacepublicv1.InstalledPlugin{Slug: plugin.Slug, Enabled: plugin.Enabled, Configuration: configuration, Version: uint32(plugin.Version), RedactedParameters: plugin.RedactedParameters}, nil
}

func releaseMessage(release application.PluginRelease) (*marketplacepublicv1.PluginRelease, error) {
	return releaseMessageForExecution(release, false)
}

// summaryMessage projects the public release onto what a catalog list shows:
// the same presentation and parameter declarations, and component counts in
// place of every component declaration.
func summaryMessage(release application.PluginRelease) (*marketplacepublicv1.PluginSummary, error) {
	full, err := releaseMessage(release)
	if err != nil {
		return nil, err
	}
	return &marketplacepublicv1.PluginSummary{
		Slug: full.Slug, Source: full.Source, Version: full.Version, Description: full.Description, NativeEntry: full.NativeEntry,
		SkillCount: uint32(len(release.Skills)), AgentCount: uint32(len(release.Agents)), McpServerCount: uint32(len(release.MCPServers)),
		Parameters: full.Parameters,
	}, nil
}

func releaseMessageForExecution(release application.PluginRelease, execution bool) (*marketplacepublicv1.PluginRelease, error) {
	result := &marketplacepublicv1.PluginRelease{
		Slug: release.Slug, Source: release.Source, Version: release.Version,
		Description: release.Description, RetrievalRef: release.RetrievalRef,
		NativeEntry: append([]byte(nil), release.NativeEntry...),
		Parameters:  make(map[string]*marketplacepublicv1.ParameterDeclaration, len(release.Parameters)),
	}
	for _, skill := range release.Skills {
		result.Skills = append(result.Skills, &marketplacepublicv1.SkillComponent{Name: skill.Name, Description: skill.Description, ArgumentHint: skill.ArgumentHint})
	}
	for _, agent := range release.Agents {
		result.Agents = append(result.Agents, &marketplacepublicv1.AgentComponent{Name: agent.Name, Description: agent.Description})
	}
	for _, server := range release.MCPServers {
		result.McpServers = append(result.McpServers, &marketplacepublicv1.MCPComponent{Name: server.Name, Definition: append([]byte(nil), server.Definition...), Relationship: relationshipMessage(server.Relationship)})
	}
	if !execution {
		sensitive := false
		for _, d := range release.Parameters {
			sensitive = sensitive || d.Sensitive
		}
		result.NativeEntry = pluginconfig.PublicNativeEntry(result.NativeEntry, sensitive)
	}
	for name, declaration := range release.Parameters {
		parameter := &marketplacepublicv1.ParameterDeclaration{
			Type: declaration.Type, Title: declaration.Title, Description: declaration.Description,
			HasDefault: declaration.Default != nil, Required: declaration.Required, Sensitive: declaration.Sensitive, Multiple: declaration.Multiple,
			Options: append([]string(nil), declaration.Options...), Min: declaration.Min, Max: declaration.Max,
		}
		if declaration.Default != nil && (execution || !declaration.Sensitive) {
			// Defaults have passed catalog declaration validation before publication.
			value, err := structpb.NewValue(wireValue(declaration.Default))
			if err != nil {
				return nil, errors.New("marketplace: cannot encode parameter declaration")
			}
			parameter.Default = value
		}
		result.Parameters[name] = parameter
	}
	return result, nil
}

func wireValue(value any) any {
	if values, ok := value.([]string); ok {
		result := make([]any, len(values))
		for index, value := range values {
			result[index] = value
		}
		return result
	}
	return value
}

func relationshipMessage(value *application.ConnectorRelationship) *marketplacepublicv1.ConnectorProviderRelationship {
	if value == nil {
		return &marketplacepublicv1.ConnectorProviderRelationship{Status: "unavailable", Reason: "relationship_not_registered"}
	}
	return &marketplacepublicv1.ConnectorProviderRelationship{ConnectorId: value.ConnectorID, ConnectorName: value.ConnectorName, Provider: value.Provider, Target: value.Target, Authentication: value.Authentication, Status: value.Status, Reason: value.Reason}
}
