package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func (h *Handler) BeginProviderAuthorization(ctx context.Context, req *connect.Request[marketplacepublicv1.BeginProviderAuthorizationRequest]) (*connect.Response[marketplacepublicv1.BeginProviderAuthorizationResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	provider, err := connectionProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	result, err := h.application.BeginPersonalAuthorization(ctx, application.BeginProviderAuthorizationInput{
		OwnerUserID: actor, Provider: provider, RedirectURI: req.Msg.GetRedirectUri(),
	})
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.BeginProviderAuthorizationResponse{AuthorizationUrl: result.AuthorizationURL}), nil
}

func (h *Handler) CompleteProviderAuthorization(ctx context.Context, req *connect.Request[marketplacepublicv1.CompleteProviderAuthorizationRequest]) (*connect.Response[marketplacepublicv1.CompleteProviderAuthorizationResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	provider, err := connectionProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	result, err := h.application.CompletePersonalAuthorization(ctx, application.CompleteProviderAuthorizationInput{
		OwnerUserID: actor, Provider: provider, Code: req.Msg.GetCode(), State: req.Msg.GetState(),
		RedirectURI: req.Msg.GetRedirectUri(),
	})
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.CompleteProviderAuthorizationResponse{Authorization: &marketplacepublicv1.ProviderAuthorization{
		Id: result.ID, Status: result.Status,
		Provider: result.Provider, ProviderAccountId: result.ProviderAccountID, ProviderLogin: result.ProviderLogin,
		OwnerUserId: result.OwnerUserID,
	}}), nil
}

func (h *Handler) GetProviderConnectionState(ctx context.Context, req *connect.Request[marketplacepublicv1.GetProviderConnectionStateRequest]) (*connect.Response[marketplacepublicv1.GetProviderConnectionStateResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	provider, err := connectionProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	state, err := h.application.GetPersonalConnection(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.GetProviderConnectionStateResponse{State: connectionStateMessage(state)}), nil
}

func (h *Handler) ListAccessibleRepositories(ctx context.Context, req *connect.Request[marketplacepublicv1.ListAccessibleRepositoriesRequest]) (*connect.Response[marketplacepublicv1.ListAccessibleRepositoriesResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	provider, err := connectionProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	repositories, err := h.application.ListAccessibleRepositories(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.ListAccessibleRepositoriesResponse{Repositories: accessibleRepositoryMessages(repositories)}), nil
}

func (h *Handler) GetProviderAccess(ctx context.Context, req *connect.Request[marketplacepublicv1.GetProviderAccessRequest]) (*connect.Response[marketplacepublicv1.GetProviderAccessResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	provider, err := connectionProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	view, err := h.application.GetProviderAccess(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(providerAccessMessage(view)), nil
}

func (h *Handler) RefreshProviderAccess(ctx context.Context, req *connect.Request[marketplacepublicv1.RefreshProviderAccessRequest]) (*connect.Response[marketplacepublicv1.RefreshProviderAccessResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	provider, err := connectionProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	view, err := h.application.RefreshProviderAccess(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.RefreshProviderAccessResponse{Access: providerAccessMessage(view)}), nil
}

func providerAccessMessage(view application.ProviderAccessView) *marketplacepublicv1.GetProviderAccessResponse {
	response := &marketplacepublicv1.GetProviderAccessResponse{State: connectionStateMessage(view.State), ManageUrl: view.ManageURL, AccessUnavailable: view.AccessUnavailable}
	if !view.ObservedAt.IsZero() {
		response.ObservedAtUnixMs = view.ObservedAt.UnixMilli()
	}
	for _, account := range view.Accounts {
		response.Accounts = append(response.Accounts, &marketplacepublicv1.ProviderAccount{Login: account.Login, Type: account.Type, AvatarUrl: account.AvatarURL,
			RepositorySelection: account.RepositorySelection, SettingsUrl: account.SettingsURL, Repositories: accessibleRepositoryMessages(account.Repositories)})
	}
	for _, permission := range view.Permissions {
		response.Permissions = append(response.Permissions, &marketplacepublicv1.ProviderPermission{Name: permission.Name, Access: permission.Access})
	}
	return response
}

func connectionProvider(value string) (domain.Provider, error) {
	switch strings.TrimSpace(value) {
	case "", string(domain.ProviderGitHub):
		return domain.ProviderGitHub, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, application.ErrProviderUnavailable)
	}
}
