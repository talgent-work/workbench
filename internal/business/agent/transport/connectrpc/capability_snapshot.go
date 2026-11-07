package connectrpc

import (
	"slices"

	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"google.golang.org/protobuf/types/known/structpb"
)

func nativeValue(value any) (*structpb.Value, error) {
	if list, ok := value.([]string); ok {
		items := make([]any, len(list))
		for i, text := range list {
			items[i] = text
		}
		value = items
	}
	return structpb.NewValue(value)
}

func capabilitySnapshotToProto(value domain.CapabilitySnapshot) (*agentprivatev1.WorkCapabilities, error) {
	result := &agentprivatev1.WorkCapabilities{Prompt: value.Prompt, StartupAgent: value.StartupAgent}
	for _, selected := range value.Plugins {
		p := selected.Release
		release := &marketplacepublicv1.PluginRelease{Slug: p.Slug, Source: p.Source, Version: p.Version, RetrievalRef: p.RetrievalRef, NativeEntry: slices.Clone(p.NativeEntry), Parameters: map[string]*marketplacepublicv1.ParameterDeclaration{}}
		for _, c := range p.Skills {
			release.Skills = append(release.Skills, &marketplacepublicv1.SkillComponent{Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint})
		}
		for _, c := range p.Agents {
			release.Agents = append(release.Agents, &marketplacepublicv1.AgentComponent{Name: c.Name, Description: c.Description})
		}
		for _, c := range p.MCPServers {
			fact := c.Relationship
			release.McpServers = append(release.McpServers, &marketplacepublicv1.MCPComponent{Name: c.Name, Definition: slices.Clone(c.Definition), Relationship: &marketplacepublicv1.ConnectorProviderRelationship{ConnectorId: fact.ConnectorID, Provider: fact.Provider, Target: fact.Target, Authentication: fact.Authentication, Status: fact.Status, Reason: fact.Reason}})
		}
		sensitive := false
		for _, d := range p.Parameters {
			sensitive = sensitive || d.Sensitive
		}
		release.NativeEntry = pluginconfig.PublicNativeEntry(release.NativeEntry, sensitive)
		for name, d := range p.Parameters {
			declaration := &marketplacepublicv1.ParameterDeclaration{Type: d.Type, Title: d.Title, Description: d.Description, HasDefault: d.Default != nil, Required: d.Required, Sensitive: d.Sensitive, Multiple: d.Multiple, Options: slices.Clone(d.Options)}
			if d.Default != nil && !d.Sensitive {
				value, err := nativeValue(d.Default)
				if err != nil {
					return nil, err
				}
				declaration.Default = value
			}
			if d.Min != nil {
				value := *d.Min
				declaration.Min = &value
			}
			if d.Max != nil {
				value := *d.Max
				declaration.Max = &value
			}
			release.Parameters[name] = declaration
		}
		configuration := &structpb.Struct{Fields: map[string]*structpb.Value{}}
		for name, value := range selected.Configuration {
			if declaration, known := selected.Release.Parameters[name]; !known || declaration.Sensitive {
				continue
			}
			encoded, err := nativeValue(value)
			if err != nil {
				return nil, err
			}
			configuration.Fields[name] = encoded
		}
		result.Plugins = append(result.Plugins, &agentprivatev1.SelectedPlugin{Release: release, Configuration: configuration})
	}
	return result, nil
}
