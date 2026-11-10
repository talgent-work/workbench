package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type SelectionHandler struct {
	app            *application.Application
	authentication *CredentialHandler
}

func NewSelectionHandler(app *application.Application, token string) *SelectionHandler {
	return &SelectionHandler{app: app, authentication: NewCredentialHandler(nil, token)}
}
func (h *SelectionHandler) ReadPersonalPlugins(ctx context.Context, request *marketplaceprivatev1.ReadPersonalPluginsRequest) (*marketplaceprivatev1.ReadPersonalPluginsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authentication.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	actor, ok := authn.DelegatedUserID(info.RequestHeader())
	if !ok || actor == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "trusted owner required")
	}
	page, err := h.app.ReadPersonalSelection(ctx, actor, request.GetCursor(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	result := &marketplaceprivatev1.ReadPersonalPluginsResponse{NextCursor: page.NextCursor}
	for _, plugin := range page.Plugins {
		message, err := installationMessage(plugin)
		if err != nil {
			return nil, mapError(err)
		}
		result.Plugins = append(result.Plugins, message)
	}
	return result, nil
}
func (h *SelectionHandler) ResolvePlugin(ctx context.Context, request *marketplaceprivatev1.ResolvePluginRequest) (*marketplaceprivatev1.ResolvePluginResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authentication.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	if actor, ok := authn.DelegatedUserID(info.RequestHeader()); !ok || actor == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "trusted owner required")
	}
	release, err := h.app.GetRelease(ctx, request.GetSlug())
	if err != nil {
		return nil, mapError(err)
	}
	message, err := releaseMessageForExecution(release, true)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplaceprivatev1.ResolvePluginResponse{Plugin: message}, nil
}
func (h *SelectionHandler) ReadConnectorSelection(ctx context.Context, request *marketplaceprivatev1.ReadConnectorSelectionRequest) (*marketplaceprivatev1.ReadConnectorSelectionResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authentication.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	actor, ok := authn.DelegatedUserID(info.RequestHeader())
	if !ok || actor == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "trusted owner required")
	}
	selection, err := h.app.ReadConnectorSelection(ctx, actor)
	if err != nil {
		return nil, mapError(err)
	}
	result := &marketplaceprivatev1.ReadConnectorSelectionResponse{EnabledConnectorIds: selection.EnabledConnectorIDs}
	for _, c := range selection.Catalog {
		message := &marketplaceprivatev1.CatalogConnector{Id: c.ID, Name: c.Name, Required: c.Required, Offered: c.Offered, Authentication: c.Authentication,
			Type: c.Configuration.Type, Url: c.Configuration.URL, Command: c.Configuration.Command, Arguments: c.Configuration.Arguments,
			Environment: c.Configuration.Environment, Headers: c.Configuration.Headers}
		if provider := c.Provider; provider != nil {
			message.Provider, message.Target, message.ProviderAuthentication = provider.Provider, provider.Target, provider.Authentication
		}
		result.Connectors = append(result.Connectors, message)
	}
	return result, nil
}
