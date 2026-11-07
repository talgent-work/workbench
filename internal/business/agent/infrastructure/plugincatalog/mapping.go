package plugincatalog

import (
	"encoding/json"
	"errors"
	"slices"

	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

func typedValue(value any) any {
	if values, ok := value.([]any); ok {
		strings := make([]string, len(values))
		for i, entry := range values {
			text, ok := entry.(string)
			if !ok {
				return value
			}
			strings[i] = text
		}
		return strings
	}
	return value
}

func personalPlugin(record *marketplacepublicv1.InstalledPlugin) (domain.PersonalPlugin, error) {
	if record == nil || record.GetSlug() == "" {
		return domain.PersonalPlugin{}, errors.New("agent: Marketplace returned an invalid personal plugin")
	}
	configuration := pluginconfig.Configuration{}
	for name, value := range record.GetConfiguration().AsMap() {
		configuration[name] = typedValue(value)
	}
	return domain.PersonalPlugin{Slug: record.GetSlug(), Enabled: record.GetEnabled(), Configuration: configuration}, nil
}

func pluginRelease(record *marketplacepublicv1.PluginRelease) (domain.PluginRelease, error) {
	if record == nil {
		return domain.PluginRelease{}, errors.New("agent: Marketplace omitted a release")
	}
	var native struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(record.GetNativeEntry(), &native) != nil || native.Name != record.GetSlug() || (native.Version != "" && native.Version != record.GetVersion()) {
		return domain.PluginRelease{}, errors.New("agent: Marketplace returned an inconsistent native entry")
	}
	release := domain.PluginRelease{Slug: record.GetSlug(), Source: record.GetSource(), Version: record.GetVersion(), RetrievalRef: record.GetRetrievalRef(), NativeEntry: slices.Clone(record.GetNativeEntry()), Parameters: pluginconfig.ParameterDeclarations{}}
	for _, component := range record.GetSkills() {
		release.Skills = append(release.Skills, domain.SkillComponent{Name: component.GetName(), Description: component.GetDescription(), ArgumentHint: component.GetArgumentHint()})
	}
	for _, component := range record.GetAgents() {
		release.Agents = append(release.Agents, domain.AgentComponent{Name: component.GetName(), Description: component.GetDescription()})
	}
	for _, component := range record.GetMcpServers() {
		release.MCPServers = append(release.MCPServers, domain.MCPComponent{Name: component.GetName(), Definition: slices.Clone(component.GetDefinition()), Relationship: connectorRelationship(component.GetRelationship())})
	}
	for name, declaration := range record.GetParameters() {
		value := pluginconfig.ParameterDeclaration{Type: declaration.GetType(), Title: declaration.GetTitle(), Description: declaration.GetDescription(), Required: declaration.GetRequired(), Sensitive: declaration.GetSensitive(), Multiple: declaration.GetMultiple(), Options: slices.Clone(declaration.GetOptions())}
		if declaration.GetDefault() != nil {
			value.Default = typedValue(declaration.GetDefault().AsInterface())
		}
		if declaration.Min != nil {
			number := declaration.GetMin()
			value.Min = &number
		}
		if declaration.Max != nil {
			number := declaration.GetMax()
			value.Max = &number
		}
		release.Parameters[name] = value
	}
	return release, release.Validate()
}

func connectorRelationship(value *marketplacepublicv1.ConnectorProviderRelationship) domain.ConnectorProviderRelationship {
	if value == nil {
		return domain.ConnectorProviderRelationship{Status: "unavailable", Reason: "relationship_not_registered"}
	}
	return domain.ConnectorProviderRelationship{ConnectorID: value.GetConnectorId(), Provider: value.GetProvider(), Target: value.GetTarget(), Authentication: value.GetAuthentication(), Status: value.GetStatus(), Reason: value.GetReason()}
}
