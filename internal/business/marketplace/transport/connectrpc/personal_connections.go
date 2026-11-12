package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func (h *Handler) BeginProviderAuthorization(ctx context.Context, req *marketplacepublicv1.BeginProviderAuthorizationRequest) (*marketplacepublicv1.BeginProviderAuthorizationResponse, error) {
	actor, err := h.owner(ctx, req.GetApplicationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	provider, err := connectionProvider(req.GetProvider())
	if err != nil {
		return nil, err
	}
	result, err := h.application.BeginPersonalAuthorization(ctx, application.BeginProviderAuthorizationInput{
		OwnerID: actor, Provider: provider, RedirectURI: req.GetRedirectUri(),
	})
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return &marketplacepublicv1.BeginProviderAuthorizationResponse{AuthorizationUrl: result.AuthorizationURL}, nil
}

func (h *Handler) CompleteProviderAuthorization(ctx context.Context, req *marketplacepublicv1.CompleteProviderAuthorizationRequest) (*marketplacepublicv1.CompleteProviderAuthorizationResponse, error) {
	actor, err := h.owner(ctx, req.GetApplicationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	provider, err := connectionProvider(req.GetProvider())
	if err != nil {
		return nil, err
	}
	result, err := h.application.CompletePersonalAuthorization(ctx, application.CompleteProviderAuthorizationInput{
		OwnerID: actor, Provider: provider, Code: req.GetCode(), State: req.GetState(),
		RedirectURI: req.GetRedirectUri(),
	})
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return &marketplacepublicv1.CompleteProviderAuthorizationResponse{Authorization: &marketplacepublicv1.ProviderAuthorization{
		Id: result.ID, Status: result.Status,
		Provider: result.Provider, ProviderAccountId: result.ProviderAccountID, ProviderLogin: result.ProviderLogin,
		OwnerUserId: result.OwnerID,
	}}, nil
}

func (h *Handler) GetProviderConnectionState(ctx context.Context, req *marketplacepublicv1.GetProviderConnectionStateRequest) (*marketplacepublicv1.GetProviderConnectionStateResponse, error) {
	actor, err := h.owner(ctx, req.GetApplicationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	provider, err := connectionProvider(req.GetProvider())
	if err != nil {
		return nil, err
	}
	state, err := h.application.GetPersonalConnection(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return &marketplacepublicv1.GetProviderConnectionStateResponse{State: connectionStateMessage(state)}, nil
}

func (h *Handler) ListAccessibleRepositories(ctx context.Context, req *marketplacepublicv1.ListAccessibleRepositoriesRequest) (*marketplacepublicv1.ListAccessibleRepositoriesResponse, error) {
	actor, err := h.owner(ctx, req.GetApplicationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	provider, err := connectionProvider(req.GetProvider())
	if err != nil {
		return nil, err
	}
	repositories, err := h.application.ListAccessibleRepositories(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return &marketplacepublicv1.ListAccessibleRepositoriesResponse{Repositories: accessibleRepositoryMessages(repositories)}, nil
}

func (h *Handler) GetProviderAccess(ctx context.Context, req *marketplacepublicv1.GetProviderAccessRequest) (*marketplacepublicv1.GetProviderAccessResponse, error) {
	actor, err := h.owner(ctx, req.GetApplicationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	provider, err := connectionProvider(req.GetProvider())
	if err != nil {
		return nil, err
	}
	view, err := h.application.GetProviderAccess(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return providerAccessMessage(view), nil
}

func (h *Handler) RefreshProviderAccess(ctx context.Context, req *marketplacepublicv1.RefreshProviderAccessRequest) (*marketplacepublicv1.RefreshProviderAccessResponse, error) {
	actor, err := h.owner(ctx, req.GetApplicationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	provider, err := connectionProvider(req.GetProvider())
	if err != nil {
		return nil, err
	}
	view, err := h.application.RefreshProviderAccess(ctx, actor, provider)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return &marketplacepublicv1.RefreshProviderAccessResponse{Access: providerAccessMessage(view)}, nil
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
		return "", connect.NewError(connect.CodeInvalidArgument, application.ErrProviderUnavailable.Error()).WithCause(application.ErrProviderUnavailable)
	}
}
