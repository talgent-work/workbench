package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/pkg/authn"
)

type SelectionHandler struct {
	app            *application.Application
	authentication *CredentialHandler
}

func NewSelectionHandler(app *application.Application, token string) *SelectionHandler {
	return &SelectionHandler{app: app, authentication: NewCredentialHandler(nil, token)}
}
func (h *SelectionHandler) ReadPersonalPlugins(ctx context.Context, request *connect.Request[marketplaceprivatev1.ReadPersonalPluginsRequest]) (*connect.Response[marketplaceprivatev1.ReadPersonalPluginsResponse], error) {
	if err := h.authentication.authenticate(ctx, request.Header()); err != nil {
		return nil, err
	}
	actor, ok := authn.CurrentUserID(ctx)
	if !ok || actor == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("trusted owner required"))
	}
	page, err := h.app.ReadPersonalSelection(ctx, actor, request.Msg.GetCursor(), int(request.Msg.GetPageSize()))
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
	return connect.NewResponse(result), nil
}
func (h *SelectionHandler) ResolvePlugin(ctx context.Context, request *connect.Request[marketplaceprivatev1.ResolvePluginRequest]) (*connect.Response[marketplaceprivatev1.ResolvePluginResponse], error) {
	if err := h.authentication.authenticate(ctx, request.Header()); err != nil {
		return nil, err
	}
	if actor, ok := authn.CurrentUserID(ctx); !ok || actor == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("trusted owner required"))
	}
	release, err := h.app.GetRelease(ctx, request.Msg.GetSlug())
	if err != nil {
		return nil, mapError(err)
	}
	message, err := releaseMessageForExecution(release, true)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplaceprivatev1.ResolvePluginResponse{Plugin: message}), nil
}
func (h *SelectionHandler) ReadConnectorSelection(ctx context.Context, request *connect.Request[marketplaceprivatev1.ReadConnectorSelectionRequest]) (*connect.Response[marketplaceprivatev1.ReadConnectorSelectionResponse], error) {
	if err := h.authentication.authenticate(ctx, request.Header()); err != nil {
		return nil, err
	}
	actor, ok := authn.CurrentUserID(ctx)
	if !ok || actor == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("trusted owner required"))
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
	return connect.NewResponse(result), nil
}
