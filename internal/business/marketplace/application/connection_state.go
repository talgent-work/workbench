package application

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const (
	providerConnectionStatusNone                  = "none"
	providerConnectionRecommendedActionNone       = "none"
	providerConnectionRecommendedActionConnect    = "connect"
	providerConnectionRecommendedActionReconnect  = "reconnect"
	providerConnectionRecommendedActionRetryLater = "retry_later"
)

func (a *PersonalConnections) GetPersonalConnection(ctx context.Context, actor string, provider domain.Provider) (*query.ProviderConnectionState, error) {
	if actor == "" {
		return nil, ErrUnauthenticated
	}
	return a.providerQueries.GetProviderConnectionState(ctx, actor, provider)
}

func providerConnectionStateWithoutAuthorization(provider domain.Provider) *query.ProviderConnectionState {
	return &query.ProviderConnectionState{
		Provider:          string(provider),
		Status:            providerConnectionStatusNone,
		RecommendedAction: providerConnectionRecommendedActionConnect,
	}
}

func providerConnectionStateFromAuthorization(auth *domain.ProviderAuthorization) *query.ProviderConnectionState {
	state := &query.ProviderConnectionState{
		AuthorizationID:             auth.ID,
		Provider:                    string(auth.Provider),
		Status:                      string(auth.Status),
		ProviderAccountID:           auth.ProviderAccountID,
		ProviderLogin:               auth.ProviderLogin,
		RecommendedAction:           providerConnectionRecommendedActionNone,
		ReasonCode:                  auth.LastFailureCode,
		ReasonMessage:               auth.LastFailureDetail,
		AccessTokenExpiresAtUnixMS:  unixMilli(auth.ExpiresAt),
		RefreshTokenExpiresAtUnixMS: unixMilli(auth.RefreshTokenExpiresAt),
		LastCheckedAtUnixMS:         unixMilli(auth.LastCheckedAt),
		LastFailureCode:             auth.LastFailureCode,
	}
	switch auth.Status {
	case domain.ProviderAuthorizationActive:
		state.CanListRepositories = true
		if auth.LastFailureCode != "" {
			state.RecommendedAction = providerConnectionRecommendedActionRetryLater
			state.CanListRepositories = false
		}
	case domain.ProviderAuthorizationReconnectRequired, domain.ProviderAuthorizationRevoked:
		state.RecommendedAction = providerConnectionRecommendedActionReconnect
	default:
		state.RecommendedAction = providerConnectionRecommendedActionReconnect
	}
	return state
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}
