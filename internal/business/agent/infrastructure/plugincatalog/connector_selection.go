package plugincatalog

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/samber/oops"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ReadConnectorSelection reads the catalog and the delegated user's enabled
// installations through the trusted selection contract.
func (c *Client) ReadConnectorSelection(ctx context.Context, user string) (command.ConnectorSelection, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := delegated(ctx, user, &marketplaceprivatev1.ReadConnectorSelectionRequest{})
	if err != nil {
		return command.ConnectorSelection{}, err
	}
	request.Header().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.selection.ReadConnectorSelection(ctx, request)
	if err != nil {
		return command.ConnectorSelection{}, oops.With("operation", "agent.connector_selection.read").Wrap(err)
	}
	result := command.ConnectorSelection{EnabledConnectors: slices.Clone(response.Msg.GetEnabledConnectorIds())}
	for _, m := range response.Msg.GetConnectors() {
		connector := domain.CatalogConnector{Offered: m.GetOffered(), SelectedConnector: domain.SelectedConnector{
			ID: m.GetId(), Name: m.GetName(), Required: m.GetRequired(), Authentication: m.GetAuthentication(),
			Type: m.GetType(), URL: m.GetUrl(), Command: m.GetCommand(), Arguments: slices.Clone(m.GetArguments()),
			Environment: maps.Clone(m.GetEnvironment()), Headers: maps.Clone(m.GetHeaders()),
			Provider: m.GetProvider(), Target: m.GetTarget(), ProviderAuthentication: m.GetProviderAuthentication()}}
		if err := connector.Validate(); err != nil {
			return command.ConnectorSelection{}, err
		}
		result.Catalog = append(result.Catalog, connector)
	}
	return result, nil
}
