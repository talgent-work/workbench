package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

// ConnectorSelection is trusted startup input for Agent: the owner's
// Connector catalog with non-secret configuration, every Withdrawn Connector
// the owner installed by identity only, and the owner's enabled
// installations. Agent composes the Work choice.
type ConnectorSelection struct {
	Catalog []SelectableConnector
	// EnabledConnectorIDs identifies the owner's enabled installations.
	EnabledConnectorIDs []string
}

// SelectableConnector is one Connector a new Work may select while Offered,
// with the display name of its publisher, empty for a Platform Connector.
type SelectableConnector struct {
	query.CatalogConnector
	Offered   bool
	Publisher string
}

// ReadConnectorSelection is restricted to trusted Agent startup composition.
func (a *Application) ReadConnectorSelection(ctx context.Context, owner string) (ConnectorSelection, error) {
	if strings.TrimSpace(owner) == "" {
		return ConnectorSelection{}, ErrUnauthenticated
	}
	connectors, err := a.connectorQueries.ListForOwner(ctx, owner)
	if err != nil {
		return ConnectorSelection{}, err
	}
	installed, err := a.installedQueries.ListInstalled(ctx, owner)
	if err != nil {
		return ConnectorSelection{}, err
	}
	installedIDs := make(map[string]bool, len(installed))
	selection := ConnectorSelection{}
	for _, installation := range installed {
		installedIDs[installation.ConnectorID] = true
		if installation.Enabled {
			selection.EnabledConnectorIDs = append(selection.EnabledConnectorIDs, installation.ConnectorID)
		}
	}
	var publishers []query.ConnectorOwner
	for _, connector := range connectors {
		switch {
		case connector.InCatalogOf(owner):
			// Provide connection: a Connector in the owner's catalog.
			selection.Catalog = append(selection.Catalog, SelectableConnector{CatalogConnector: connector, Offered: true})
			publishers = append(publishers, connector.Owner)
		case installedIDs[connector.ID]:
			// Only its identity, so Agent refuses a choice of it.
			selection.Catalog = append(selection.Catalog, SelectableConnector{CatalogConnector: query.CatalogConnector{ID: connector.ID, Name: connector.Name, Required: connector.Required}})
		}
	}
	names := a.publisherNames(ctx, publishers)
	for i := range selection.Catalog {
		selection.Catalog[i].Publisher = publisherName(names, selection.Catalog[i].Owner)
		if provider := selection.Catalog[i].Provider; provider != nil {
			provider.TargetCredentialsExpire = a.targetCredentialsExpire(provider.Provider)
		}
	}
	return selection, nil
}
