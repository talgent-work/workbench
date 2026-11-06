package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/pkg/authn"
)

type Handler struct{ application *application.Application }

var _ marketplacepublicv1connect.MarketplaceServiceHandler = (*Handler)(nil)

func NewHandler(app *application.Application) *Handler { return &Handler{application: app} }

func (h *Handler) ListPlugins(ctx context.Context, request *connect.Request[marketplacepublicv1.ListPluginsRequest]) (*connect.Response[marketplacepublicv1.ListPluginsResponse], error) {
	page, err := h.application.ListReleases(ctx, request.Msg.GetCursor(), int(request.Msg.GetPageSize()), request.Msg.GetComponentType())
	if err != nil {
		return nil, mapError(err)
	}
	result := &marketplacepublicv1.ListPluginsResponse{NextCursor: page.NextCursor}
	for _, plugin := range page.Releases {
		message, err := summaryMessage(plugin)
		if err != nil {
			return nil, mapError(err)
		}
		result.Plugins = append(result.Plugins, message)
	}
	return connect.NewResponse(result), nil
}
func (h *Handler) GetPlugin(ctx context.Context, request *connect.Request[marketplacepublicv1.GetPluginRequest]) (*connect.Response[marketplacepublicv1.GetPluginResponse], error) {
	plugin, err := h.application.GetRelease(ctx, request.Msg.GetSlug())
	if err != nil {
		return nil, mapError(err)
	}
	message, err := releaseMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.GetPluginResponse{Plugin: message}), nil
}
func (h *Handler) ListInstalledPlugins(ctx context.Context, request *connect.Request[marketplacepublicv1.ListInstalledPluginsRequest]) (*connect.Response[marketplacepublicv1.ListInstalledPluginsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	page, err := h.application.ListInstallations(ctx, actor, request.Msg.GetCursor(), int(request.Msg.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	result := &marketplacepublicv1.ListInstalledPluginsResponse{NextCursor: page.NextCursor}
	for _, plugin := range page.Plugins {
		message, err := installationMessage(plugin)
		if err != nil {
			return nil, mapError(err)
		}
		result.Plugins = append(result.Plugins, message)
	}
	return connect.NewResponse(result), nil
}
func (h *Handler) GetInstalledPlugin(ctx context.Context, request *connect.Request[marketplacepublicv1.GetInstalledPluginRequest]) (*connect.Response[marketplacepublicv1.GetInstalledPluginResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	plugin, err := h.application.GetInstallation(ctx, actor, request.Msg.GetSlug())
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.GetInstalledPluginResponse{Plugin: message}), nil
}
func (h *Handler) InstallPlugin(ctx context.Context, request *connect.Request[marketplacepublicv1.InstallPluginRequest]) (*connect.Response[marketplacepublicv1.InstallPluginResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	plugin, err := h.application.Install(ctx, application.InstallPlugin{ActorUserID: actor, Slug: request.Msg.GetSlug(), Configuration: request.Msg.GetConfiguration().AsMap()})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.InstallPluginResponse{Plugin: message}), nil
}
func (h *Handler) EnablePlugin(ctx context.Context, request *connect.Request[marketplacepublicv1.EnablePluginRequest]) (*connect.Response[marketplacepublicv1.EnablePluginResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	plugin, err := h.application.Enable(ctx, application.InstallationCommand{ActorUserID: actor, Slug: request.Msg.GetSlug(), Version: int(request.Msg.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.EnablePluginResponse{Plugin: message}), nil
}
func (h *Handler) DisablePlugin(ctx context.Context, request *connect.Request[marketplacepublicv1.DisablePluginRequest]) (*connect.Response[marketplacepublicv1.DisablePluginResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	plugin, err := h.application.Disable(ctx, application.InstallationCommand{ActorUserID: actor, Slug: request.Msg.GetSlug(), Version: int(request.Msg.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.DisablePluginResponse{Plugin: message}), nil
}
func (h *Handler) SetPluginConfiguration(ctx context.Context, request *connect.Request[marketplacepublicv1.SetPluginConfigurationRequest]) (*connect.Response[marketplacepublicv1.SetPluginConfigurationResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	plugin, err := h.application.SetConfiguration(ctx, application.SetConfiguration{InstallationCommand: application.InstallationCommand{ActorUserID: actor, Slug: request.Msg.GetSlug(), Version: int(request.Msg.GetVersion())}, Values: request.Msg.GetValues().AsMap()})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.SetPluginConfigurationResponse{Plugin: message}), nil
}
func (h *Handler) DeletePluginConfiguration(ctx context.Context, request *connect.Request[marketplacepublicv1.DeletePluginConfigurationRequest]) (*connect.Response[marketplacepublicv1.DeletePluginConfigurationResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	plugin, err := h.application.DeleteConfiguration(ctx, application.DeleteConfiguration{InstallationCommand: application.InstallationCommand{ActorUserID: actor, Slug: request.Msg.GetSlug(), Version: int(request.Msg.GetVersion())}, Names: request.Msg.GetNames()})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.DeletePluginConfigurationResponse{Plugin: message}), nil
}
func (h *Handler) UninstallPlugin(ctx context.Context, request *connect.Request[marketplacepublicv1.UninstallPluginRequest]) (*connect.Response[marketplacepublicv1.UninstallPluginResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	err := h.application.Uninstall(ctx, application.InstallationCommand{ActorUserID: actor, Slug: request.Msg.GetSlug(), Version: int(request.Msg.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.UninstallPluginResponse{}), nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, application.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, domain.ErrConcurrentModification):
		return connect.NewError(connect.CodeAborted, err)
	case errors.Is(err, domain.ErrConnectorNotFound), errors.Is(err, domain.ErrInstallationNotFound), errors.Is(err, domain.ErrConnectorInstallationNotFound), errors.Is(err, application.ErrReleaseNotFound), errors.Is(err, domain.ErrProviderNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrConnectorNotInstallable):
		return connect.NewError(connect.CodeFailedPrecondition, domain.ErrConnectorNotInstallable)
	case errors.Is(err, domain.ErrAuthorizationReconnectRequired):
		return connect.NewError(connect.CodeFailedPrecondition, domain.ErrAuthorizationReconnectRequired)
	case errors.Is(err, domain.ErrRevocationUnconfirmed):
		return connect.NewError(connect.CodeUnavailable, domain.ErrRevocationUnconfirmed)
	case errors.Is(err, domain.ErrInvalidConfiguration), errors.Is(err, domain.ErrInvalidInstallation), errors.Is(err, application.ErrInvalidQuery):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, privateError{err})
	}
}

// Preserve the cause for execution logging without publishing provider details.
type privateError struct{ cause error }

func (privateError) Error() string   { return "marketplace request failed" }
func (e privateError) Unwrap() error { return e.cause }
