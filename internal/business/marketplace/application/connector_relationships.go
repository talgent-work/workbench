package application

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

// ConnectorRelationship relates one Plugin MCP declaration to the catalog
// Connector addressing the same endpoint. It is display information: it neither
// governs credential supply nor merges the declaration into the Connector.
type ConnectorRelationship struct {
	ConnectorID, ConnectorName, Provider, Target, Authentication, Status, Reason string
}

// connectorEndpoints indexes Offered catalog entries by Claude Code endpoint
// equivalence: the URL for remote servers, command and arguments for stdio.
type connectorEndpoints map[string][]query.CatalogConnector

func indexConnectorEndpoints(catalog []query.CatalogConnector) connectorEndpoints {
	index := connectorEndpoints{}
	for _, connector := range catalog {
		if !connector.Offered {
			continue
		}
		c := connector.Configuration
		key := endpointKey(c.Type, c.URL, c.Command, c.Arguments)
		index[key] = append(index[key], connector)
	}
	return index
}

func (index connectorEndpoints) relate(definition []byte) ConnectorRelationship {
	var native struct {
		Type    string   `json:"type"`
		URL     string   `json:"url"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(definition, &native) != nil {
		return ConnectorRelationship{Status: "unavailable", Reason: "relationship_not_registered"}
	}
	matches := index[endpointKey(native.Type, native.URL, native.Command, native.Args)]
	switch len(matches) {
	case 0:
		return ConnectorRelationship{Status: "unavailable", Reason: "relationship_not_registered"}
	case 1:
		connector := matches[0]
		relationship := ConnectorRelationship{ConnectorID: connector.ID, ConnectorName: connector.Name, Authentication: connector.Authentication, Status: "supported"}
		if provider := connector.Provider; provider != nil {
			relationship.Provider, relationship.Target, relationship.Authentication = provider.Provider, provider.Target, provider.Authentication
		}
		return relationship
	default:
		return ConnectorRelationship{Status: "unavailable", Reason: "relationship_ambiguous"}
	}
}

// usedBy reports whether any MCP declaration of the release addresses the connector.
func (index connectorEndpoints) usedBy(release PluginRelease, connectorID string) bool {
	return slices.ContainsFunc(release.MCPServers, func(component MCPComponent) bool {
		return index.relate(component.Definition).ConnectorID == connectorID
	})
}

func endpointKey(kind, url, command string, arguments []string) string {
	if url != "" && kind != "stdio" {
		return "url\x00" + strings.TrimRight(url, "/")
	}
	return "stdio\x00" + command + "\x00" + strings.Join(arguments, "\x00")
}
