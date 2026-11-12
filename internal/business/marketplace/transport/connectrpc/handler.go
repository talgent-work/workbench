package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type Handler struct {
	application *application.Application
	owners      *application.Owners
}

var _ marketplacepublicv1connect.MarketplaceServiceHandler = (*Handler)(nil)

func NewHandler(app *application.Application, owners *application.Owners) *Handler {
	return &Handler{application: app, owners: owners}
}

// owner is whose installations or Provider Authorizations the request
// reaches: the caller's own, or an Application the calling person owns.
func (h *Handler) owner(ctx context.Context, applicationID string) (string, error) {
	caller := application.Caller{}
	if found, ok := authn.CurrentApplication(ctx); ok {
		caller.ApplicationID = found.ID
	} else {
		caller.UserID, _ = authn.CurrentUserID(ctx)
	}
	return h.owners.Resolve(ctx, caller, applicationID)
}

func (h *Handler) ListPlugins(ctx context.Context, request *marketplacepublicv1.ListPluginsRequest) (*marketplacepublicv1.ListPluginsResponse, error) {
	page, err := h.application.ListReleases(ctx, request.GetCursor(), int(request.GetPageSize()), request.GetComponentType())
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
	return result, nil
}
func (h *Handler) GetPlugin(ctx context.Context, request *marketplacepublicv1.GetPluginRequest) (*marketplacepublicv1.GetPluginResponse, error) {
	plugin, err := h.application.GetRelease(ctx, request.GetSlug())
	if err != nil {
		return nil, mapError(err)
	}
	message, err := releaseMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.GetPluginResponse{Plugin: message}, nil
}
func (h *Handler) ListInstalledPlugins(ctx context.Context, request *marketplacepublicv1.ListInstalledPluginsRequest) (*marketplacepublicv1.ListInstalledPluginsResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	page, err := h.application.ListInstallations(ctx, owner, request.GetCursor(), int(request.GetPageSize()))
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
	return result, nil
}
func (h *Handler) GetInstalledPlugin(ctx context.Context, request *marketplacepublicv1.GetInstalledPluginRequest) (*marketplacepublicv1.GetInstalledPluginResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	plugin, err := h.application.GetInstallation(ctx, owner, request.GetSlug())
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.GetInstalledPluginResponse{Plugin: message}, nil
}
func (h *Handler) InstallPlugin(ctx context.Context, request *marketplacepublicv1.InstallPluginRequest) (*marketplacepublicv1.InstallPluginResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	plugin, err := h.application.Install(ctx, application.InstallPlugin{OwnerID: owner, Slug: request.GetSlug(), Configuration: request.GetConfiguration().AsMap()})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.InstallPluginResponse{Plugin: message}, nil
}
func (h *Handler) EnablePlugin(ctx context.Context, request *marketplacepublicv1.EnablePluginRequest) (*marketplacepublicv1.EnablePluginResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	plugin, err := h.application.Enable(ctx, application.InstallationCommand{OwnerID: owner, Slug: request.GetSlug(), Version: int(request.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.EnablePluginResponse{Plugin: message}, nil
}
func (h *Handler) DisablePlugin(ctx context.Context, request *marketplacepublicv1.DisablePluginRequest) (*marketplacepublicv1.DisablePluginResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	plugin, err := h.application.Disable(ctx, application.InstallationCommand{OwnerID: owner, Slug: request.GetSlug(), Version: int(request.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.DisablePluginResponse{Plugin: message}, nil
}
func (h *Handler) SetPluginConfiguration(ctx context.Context, request *marketplacepublicv1.SetPluginConfigurationRequest) (*marketplacepublicv1.SetPluginConfigurationResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	plugin, err := h.application.SetConfiguration(ctx, application.SetConfiguration{InstallationCommand: application.InstallationCommand{OwnerID: owner, Slug: request.GetSlug(), Version: int(request.GetVersion())}, Values: request.GetValues().AsMap()})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.SetPluginConfigurationResponse{Plugin: message}, nil
}
func (h *Handler) DeletePluginConfiguration(ctx context.Context, request *marketplacepublicv1.DeletePluginConfigurationRequest) (*marketplacepublicv1.DeletePluginConfigurationResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	plugin, err := h.application.DeleteConfiguration(ctx, application.DeleteConfiguration{InstallationCommand: application.InstallationCommand{OwnerID: owner, Slug: request.GetSlug(), Version: int(request.GetVersion())}, Names: request.GetNames()})
	if err != nil {
		return nil, mapError(err)
	}
	message, err := installationMessage(plugin)
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.DeletePluginConfigurationResponse{Plugin: message}, nil
}
func (h *Handler) UninstallPlugin(ctx context.Context, request *marketplacepublicv1.UninstallPluginRequest) (*marketplacepublicv1.UninstallPluginResponse, error) {
	owner, err := h.owner(ctx, request.GetApplicationId())
	if err != nil {
		return nil, mapError(err)
	}
	err = h.application.Uninstall(ctx, application.InstallationCommand{OwnerID: owner, Slug: request.GetSlug(), Version: int(request.GetVersion())})
	if err != nil {
		return nil, mapError(err)
	}
	return &marketplacepublicv1.UninstallPluginResponse{}, nil
}

func mapError(err error) error {
	switch {
	// A caller that gave up, or ran out of time, is not a Marketplace failure.
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err.Error()).WithCause(err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err.Error()).WithCause(err)
	case errors.Is(err, application.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrConcurrentModification):
		return connect.NewError(connect.CodeAborted, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrConnectorNotFound), errors.Is(err, domain.ErrInstallationNotFound), errors.Is(err, domain.ErrConnectorInstallationNotFound), errors.Is(err, application.ErrReleaseNotFound), errors.Is(err, domain.ErrProviderNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrConnectorNotInstallable):
		return connect.NewError(connect.CodeFailedPrecondition, domain.ErrConnectorNotInstallable.Error()).WithCause(domain.ErrConnectorNotInstallable)
	case errors.Is(err, domain.ErrAuthorizationReconnectRequired):
		return connect.NewError(connect.CodeFailedPrecondition, domain.ErrAuthorizationReconnectRequired.Error()).WithCause(domain.ErrAuthorizationReconnectRequired)
	case errors.Is(err, domain.ErrRevocationUnconfirmed):
		return connect.NewError(connect.CodeUnavailable, domain.ErrRevocationUnconfirmed.Error()).WithCause(domain.ErrRevocationUnconfirmed)
	case errors.Is(err, domain.ErrInvalidConfiguration), errors.Is(err, domain.ErrInvalidInstallation), errors.Is(err, application.ErrInvalidQuery):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	default:
		return connect.NewError(connect.CodeInternal, privateError{err}.Error())
	}
}

// Preserve the cause for execution logging without publishing provider details.
type privateError struct{ cause error }

func (privateError) Error() string   { return "marketplace request failed" }
func (e privateError) Unwrap() error { return e.cause }
