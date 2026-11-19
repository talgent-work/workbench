package connectrpc

import (
	"context"

	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func (h *Handler) ListOwnedConnectors(ctx context.Context, request *marketplacepublicv1.ListOwnedConnectorsRequest) (*marketplacepublicv1.ListOwnedConnectorsResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	connectors, err := h.application.ListOwnedConnectors(ctx, owner)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ListOwnedConnectorsResponse{Connectors: connectorMessages(connectors)}, nil
}

func (h *Handler) AddConnector(ctx context.Context, request *marketplacepublicv1.AddConnectorRequest) (*marketplacepublicv1.AddConnectorResponse, error) {
	owner, err := h.connectorOwner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	configuration, err := connectorConfiguration(request.GetTransport(), request.GetSettings())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.AddConnector(ctx, application.ConnectorAuthoring{Owner: owner, Name: request.GetName(), Configuration: configuration, Provider: request.GetProvider(),
		Presentation: domain.CatalogPresentation{Description: request.GetDescription(), Category: request.GetCategory(), Icon: connectorIcon(request.GetIcon())}})
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.AddConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) ChangeConnector(ctx context.Context, request *marketplacepublicv1.ChangeConnectorRequest) (*marketplacepublicv1.ChangeConnectorResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	settings := request.GetSettings()
	detail, err := h.application.ChangeConnector(ctx, application.ConnectorChange{OwnerID: owner, ConnectorID: request.GetConnectorId(), Version: int(request.GetVersion()),
		URL: settings.GetUrl(), Command: settings.GetCommand(), Arguments: settings.GetArguments(), Description: request.GetDescription(), Category: request.GetCategory()})
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ChangeConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) ChangeConnectorIcon(ctx context.Context, request *marketplacepublicv1.ChangeConnectorIconRequest) (*marketplacepublicv1.ChangeConnectorIconResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.ChangeConnectorIcon(ctx, application.ConnectorIconChange{OwnerID: owner, ConnectorID: request.GetConnectorId(), Version: int(request.GetVersion()), Icon: connectorIcon(request.GetIcon())})
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.ChangeConnectorIconResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) GetConnectorIcon(ctx context.Context, request *marketplacepublicv1.GetConnectorIconRequest) (*marketplacepublicv1.GetConnectorIconResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	icon, err := h.application.GetConnectorIcon(ctx, owner, request.GetConnectorId())
	if err != nil {
		return nil, mapError(err)
	}
	if icon == nil {
		return &marketplacepublicv1.GetConnectorIconResponse{}, nil
	}
	return &marketplacepublicv1.GetConnectorIconResponse{Icon: &marketplacepublicv1.ConnectorIcon{MediaType: icon.MediaType, Data: icon.Data}, Digest: icon.Digest()}, nil
}

func (h *Handler) PublishConnector(ctx context.Context, request *marketplacepublicv1.PublishConnectorRequest) (*marketplacepublicv1.PublishConnectorResponse, error) {
	command, err := h.connectorCommand(ctx, request.GetApplicationId(), request.GetConnectorId(), request.GetVersion())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.PublishConnector(ctx, command)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.PublishConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) WithdrawConnector(ctx context.Context, request *marketplacepublicv1.WithdrawConnectorRequest) (*marketplacepublicv1.WithdrawConnectorResponse, error) {
	command, err := h.connectorCommand(ctx, request.GetApplicationId(), request.GetConnectorId(), request.GetVersion())
	if err != nil {
		return nil, mapError(err)
	}
	detail, err := h.application.WithdrawConnector(ctx, command)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.WithdrawConnectorResponse{Connector: connectorMessage(detail.Connector)}, nil
}

func (h *Handler) DeleteConnector(ctx context.Context, request *marketplacepublicv1.DeleteConnectorRequest) (*marketplacepublicv1.DeleteConnectorResponse, error) {
	command, err := h.connectorCommand(ctx, request.GetApplicationId(), request.GetConnectorId(), request.GetVersion())
	if err != nil {
		return nil, mapError(err)
	}
	if err := h.application.DeleteConnector(ctx, command); err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.DeleteConnectorResponse{}, nil
}

func (h *Handler) connectorCommand(ctx context.Context, applicationID, connectorID string, version uint32) (application.ConnectorCommand, error) {
	owner, err := h.owner(ctx, applicationID)
	return application.ConnectorCommand{OwnerID: owner, ConnectorID: connectorID, Version: int(version)}, err
}

// connectorConfiguration maps an owner's connection: an address for a remote
// server, a command for a local one. Headers and environment are never taken.
func connectorConfiguration(transport string, settings *marketplacepublicv1.ConnectorSettings) (domain.MCPConfiguration, error) {
	switch transport {
	case "http":
		return domain.MCPConfiguration{Type: "http", URL: settings.GetUrl()}, nil
	case "stdio":
		return domain.MCPConfiguration{Type: "stdio", Command: settings.GetCommand(), Arguments: settings.GetArguments()}, nil
	default:
		return domain.MCPConfiguration{}, domain.ErrInvalidConnector
	}
}

func connectorIcon(icon *marketplacepublicv1.ConnectorIcon) *domain.ConnectorIcon {
	if icon == nil {
		return nil
	}
	return &domain.ConnectorIcon{MediaType: icon.GetMediaType(), Data: icon.GetData()}
}
