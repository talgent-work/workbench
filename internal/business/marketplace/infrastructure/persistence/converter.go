package persistence

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func providerAuthorizationToDO(a *domain.ProviderAuthorization) (ProviderAuthorizationDO, error) {
	scope, err := accessScopeToJSON(a.AccessScope)
	if err != nil {
		return ProviderAuthorizationDO{}, err
	}
	return ProviderAuthorizationDO{
		ID:                     a.ID,
		OwnerID:                a.OwnerID,
		Provider:               string(a.Provider),
		ProviderAccountID:      a.ProviderAccountID,
		ProviderLogin:          a.ProviderLogin,
		AccessTokenCiphertext:  a.AccessTokenCiphertext,
		RefreshTokenCiphertext: a.RefreshTokenCiphertext,
		ExpiresAt:              millisFromTime(a.ExpiresAt),
		RefreshTokenExpiresAt:  millisFromTime(a.RefreshTokenExpiresAt),
		Status:                 string(a.Status),

		LastRefreshedAt:   millisFromTime(a.LastRefreshedAt),
		LastCheckedAt:     millisFromTime(a.LastCheckedAt),
		LastFailureCode:   a.LastFailureCode,
		LastFailureDetail: a.LastFailureDetail,
		Version:           a.Version,

		AccessScopeJSON:       scope,
		AccessScopeObservedAt: millisFromTime(a.AccessScope.ObservedAt),
	}, nil
}

func providerAuthorizationFromDO(row ProviderAuthorizationDO) (*domain.ProviderAuthorization, error) {
	if row.DeletedAt != 0 {
		return nil, domain.ErrProviderNotFound
	}
	scope, err := accessScopeFromJSON(row.AccessScopeJSON, row.AccessScopeObservedAt)
	if err != nil {
		return nil, err
	}
	return domain.RehydrateProviderAuthorization(domain.ProviderAuthorization{
		AccessScope: scope,
		ID:          row.ID,
		OwnerID:     row.OwnerID,
		Provider:    domain.Provider(row.Provider),
		Status:      domain.ProviderAuthorizationStatus(row.Status),

		LastRefreshedAt:   timeFromMillis(row.LastRefreshedAt),
		LastCheckedAt:     timeFromMillis(row.LastCheckedAt),
		LastFailureCode:   row.LastFailureCode,
		LastFailureDetail: row.LastFailureDetail,
		Version:           row.Version,

		ExternalAccount: domain.ExternalAccount{
			ProviderAccountID: row.ProviderAccountID,
			ProviderLogin:     row.ProviderLogin,
		},

		CredentialSet: domain.CredentialSet{
			AccessTokenCiphertext:  row.AccessTokenCiphertext,
			RefreshTokenCiphertext: row.RefreshTokenCiphertext,
			ExpiresAt:              timeFromMillis(row.ExpiresAt),
			RefreshTokenExpiresAt:  timeFromMillis(row.RefreshTokenExpiresAt),
		},
	})
}

func timeFromMillis(millis int64) time.Time {
	if millis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
}

func millisFromTime(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
