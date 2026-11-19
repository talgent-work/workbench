package connectrpc

import (
	"context"

	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

func (h *Handler) ListConnectors(ctx context.Context, request *marketplacepublicv1.ListConnectorsRequest) (*marketplacepublicv1.ListConnectorsResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	connectors, err := h.application.ListConnectors(ctx, owner)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ListConnectorsResponse{Connectors: connectorMessages(connectors)}, nil
}

func (h *Handler) ListInstalledConnectors(ctx context.Context, request *marketplacepublicv1.ListInstalledConnectorsRequest) (*marketplacepublicv1.ListInstalledConnectorsResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	connectors, err := h.application.ListInstalledConnectors(ctx, owner)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ListInstalledConnectorsResponse{Connectors: connectorMessages(connectors)}, nil
}

func (h *Handler) GetConnector(ctx context.Context, request *marketplacepublicv1.GetConnectorRequest) (*marketplacepublicv1.GetConnectorResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.GetConnector(ctx, owner, request.GetConnectorId())
	if err != nil {
		return nil, mapError(err)
	}
	response := &marketplacepublicv1.GetConnectorResponse{Connector: connectorMessage(detail.Connector)}
	for _, plugin := range detail.Plugins {
		response.Plugins = append(response.Plugins, &marketplacepublicv1.ConnectorPlugin{Slug: plugin.Slug, Description: plugin.Description})
	}
	return response, nil
}

func (h *Handler) InstallConnector(ctx context.Context, request *marketplacepublicv1.InstallConnectorRequest) (*marketplacepublicv1.InstallConnectorResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.InstallConnector(ctx, owner, request.GetConnectorId())
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.InstallConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) EnableConnector(ctx context.Context, request *marketplacepublicv1.EnableConnectorRequest) (*marketplacepublicv1.EnableConnectorResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.EnableConnector(ctx, application.ConnectorInstallationCommand{OwnerID: owner, ConnectorID: request.GetConnectorId(), Version: int(request.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.EnableConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) DisableConnector(ctx context.Context, request *marketplacepublicv1.DisableConnectorRequest) (*marketplacepublicv1.DisableConnectorResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.DisableConnector(ctx, application.ConnectorInstallationCommand{OwnerID: owner, ConnectorID: request.GetConnectorId(), Version: int(request.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.DisableConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) UninstallConnector(ctx context.Context, request *marketplacepublicv1.UninstallConnectorRequest) (*marketplacepublicv1.UninstallConnectorResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	if err := h.application.UninstallConnector(ctx, application.ConnectorInstallationCommand{OwnerID: owner, ConnectorID: request.GetConnectorId(), Version: int(request.GetVersion())}); err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.UninstallConnectorResponse{}, nil
}

func connectorMessages(connectors []application.Connector) []*marketplacepublicv1.Connector {
	result := make([]*marketplacepublicv1.Connector, 0, len(connectors))
	for _, connector := range connectors {
		result = append(result, connectorMessage(connector))
	}
	return result
}

func connectorMessage(c application.Connector) *marketplacepublicv1.Connector {
	message := &marketplacepublicv1.Connector{ConnectorId: c.ID, Name: c.Name, Description: c.Description, Category: c.Category, Required: c.Required, Offered: c.Offered,
		State: c.State, Transport: c.Transport, IconDigest: c.IconDigest, ConnectorVersion: uint32(c.Version), PublishedAtUnixMs: c.PublishedAtMillis,
		Publisher: &marketplacepublicv1.ConnectorPublisher{Kind: c.Publisher.Kind, DisplayName: c.Publisher.DisplayName, Own: c.Publisher.Own},
		Status:    c.Status, InstallationVersion: uint32(c.InstallationVersion), Provider: c.Provider, ProviderConnection: c.ProviderConnection}
	if settings := c.Settings; settings != nil {
		message.Settings = &marketplacepublicv1.ConnectorSettings{Url: settings.URL, Command: settings.Command, Arguments: settings.Arguments}
	}
	return message
}
