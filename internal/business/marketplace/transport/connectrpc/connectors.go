package connectrpc

import (
	"context"

	"connectrpc.com/connect"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func (h *Handler) ListConnectors(ctx context.Context, _ *connect.Request[marketplacepublicv1.ListConnectorsRequest]) (*connect.Response[marketplacepublicv1.ListConnectorsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	connectors, err := h.application.ListConnectors(ctx, actor)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.ListConnectorsResponse{Connectors: connectorMessages(connectors)}), nil
}

func (h *Handler) ListInstalledConnectors(ctx context.Context, _ *connect.Request[marketplacepublicv1.ListInstalledConnectorsRequest]) (*connect.Response[marketplacepublicv1.ListInstalledConnectorsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	connectors, err := h.application.ListInstalledConnectors(ctx, actor)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.ListInstalledConnectorsResponse{Connectors: connectorMessages(connectors)}), nil
}

func (h *Handler) GetConnector(ctx context.Context, request *connect.Request[marketplacepublicv1.GetConnectorRequest]) (*connect.Response[marketplacepublicv1.GetConnectorResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	detail, err := h.application.GetConnector(ctx, actor, request.Msg.GetName())
	if err != nil {
		return nil, mapError(err)
	}
	response := &marketplacepublicv1.GetConnectorResponse{Connector: connectorMessage(detail.Connector)}
	for _, plugin := range detail.Plugins {
		response.Plugins = append(response.Plugins, &marketplacepublicv1.ConnectorPlugin{Slug: plugin.Slug, Description: plugin.Description})
	}
	return connect.NewResponse(response), nil
}

func (h *Handler) InstallConnector(ctx context.Context, request *connect.Request[marketplacepublicv1.InstallConnectorRequest]) (*connect.Response[marketplacepublicv1.InstallConnectorResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	detail, err := h.application.InstallConnector(ctx, actor, request.Msg.GetName())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.InstallConnectorResponse{Connector: connectorMessage(detail.Connector)}), nil
}

func (h *Handler) EnableConnector(ctx context.Context, request *connect.Request[marketplacepublicv1.EnableConnectorRequest]) (*connect.Response[marketplacepublicv1.EnableConnectorResponse], error) {
	detail, err := h.application.EnableConnector(ctx, connectorCommand(ctx, request.Msg.GetName(), request.Msg.GetVersion()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.EnableConnectorResponse{Connector: connectorMessage(detail.Connector)}), nil
}

func (h *Handler) DisableConnector(ctx context.Context, request *connect.Request[marketplacepublicv1.DisableConnectorRequest]) (*connect.Response[marketplacepublicv1.DisableConnectorResponse], error) {
	detail, err := h.application.DisableConnector(ctx, connectorCommand(ctx, request.Msg.GetName(), request.Msg.GetVersion()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.DisableConnectorResponse{Connector: connectorMessage(detail.Connector)}), nil
}

func (h *Handler) UninstallConnector(ctx context.Context, request *connect.Request[marketplacepublicv1.UninstallConnectorRequest]) (*connect.Response[marketplacepublicv1.UninstallConnectorResponse], error) {
	if err := h.application.UninstallConnector(ctx, connectorCommand(ctx, request.Msg.GetName(), request.Msg.GetVersion())); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.UninstallConnectorResponse{}), nil
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
