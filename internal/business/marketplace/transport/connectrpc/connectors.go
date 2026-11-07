package connectrpc

import (
	"context"

	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func (h *Handler) ListConnectors(ctx context.Context, _ *marketplacepublicv1.ListConnectorsRequest) (*marketplacepublicv1.ListConnectorsResponse, error) {
	actor, _ := authn.CurrentUserID(ctx)
	connectors, err := h.application.ListConnectors(ctx, actor)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ListConnectorsResponse{Connectors: connectorMessages(connectors)}, nil
}

func (h *Handler) ListInstalledConnectors(ctx context.Context, _ *marketplacepublicv1.ListInstalledConnectorsRequest) (*marketplacepublicv1.ListInstalledConnectorsResponse, error) {
	actor, _ := authn.CurrentUserID(ctx)
	connectors, err := h.application.ListInstalledConnectors(ctx, actor)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ListInstalledConnectorsResponse{Connectors: connectorMessages(connectors)}, nil
}

func (h *Handler) GetConnector(ctx context.Context, request *marketplacepublicv1.GetConnectorRequest) (*marketplacepublicv1.GetConnectorResponse, error) {
	actor, _ := authn.CurrentUserID(ctx)
	detail, err := h.application.GetConnector(ctx, actor, request.GetName())
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
	actor, _ := authn.CurrentUserID(ctx)
	detail, err := h.application.InstallConnector(ctx, actor, request.GetName())
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.InstallConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) EnableConnector(ctx context.Context, request *marketplacepublicv1.EnableConnectorRequest) (*marketplacepublicv1.EnableConnectorResponse, error) {
	detail, err := h.application.EnableConnector(ctx, connectorCommand(ctx, request.GetName(), request.GetVersion()))
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.EnableConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) DisableConnector(ctx context.Context, request *marketplacepublicv1.DisableConnectorRequest) (*marketplacepublicv1.DisableConnectorResponse, error) {
	detail, err := h.application.DisableConnector(ctx, connectorCommand(ctx, request.GetName(), request.GetVersion()))
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.DisableConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) UninstallConnector(ctx context.Context, request *marketplacepublicv1.UninstallConnectorRequest) (*marketplacepublicv1.UninstallConnectorResponse, error) {
	if err := h.application.UninstallConnector(ctx, connectorCommand(ctx, request.GetName(), request.GetVersion())); err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.UninstallConnectorResponse{}, nil
}

func connectorCommand(ctx context.Context, name string, version uint32) application.ConnectorInstallationCommand {
	actor, _ := authn.CurrentUserID(ctx)
	return application.ConnectorInstallationCommand{ActorUserID: actor, Name: name, Version: int(version)}
}

func connectorMessages(connectors []application.Connector) []*marketplacepublicv1.Connector {
	result := make([]*marketplacepublicv1.Connector, 0, len(connectors))
	for _, connector := range connectors {
		result = append(result, connectorMessage(connector))
	}
	return result
}

func connectorMessage(c application.Connector) *marketplacepublicv1.Connector {
	return &marketplacepublicv1.Connector{Name: c.Name, Description: c.Description, Category: c.Category, Required: c.Required, Offered: c.Offered, Status: c.Status,
		InstallationVersion: uint32(c.InstallationVersion), Provider: c.Provider, ProviderConnection: c.ProviderConnection}
}
