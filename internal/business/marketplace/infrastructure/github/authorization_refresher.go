package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/tokenmaintenance"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type AuthorizationRefresher struct {
	enabled        bool
	cfg            Config
	http           *http.Client
	authorizations application.AuthorizationStore
	cipher         TokenCipher
	maintenance    tokenmaintenance.Config
	now            func() time.Time
}

func NewAuthorizationRefresher(cfg Config, maintenance tokenmaintenance.Config, authorizations application.AuthorizationStore, cipher TokenCipher) (*AuthorizationRefresher, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	normalizedMaintenance, err := maintenance.Normalize()
	if err != nil {
		return nil, err
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, err
	}
	if enabled {
		if authorizations == nil {
			return nil, fmt.Errorf("github authorization refresher requires provider authorization repository")
		}
		if cipher == nil {
			return nil, fmt.Errorf("github authorization refresher requires token cipher")
		}
	}
	return &AuthorizationRefresher{
		enabled:        enabled,
		cfg:            cfg,
		http:           http.DefaultClient,
		authorizations: authorizations,
		cipher:         cipher,
		maintenance:    normalizedMaintenance,
		now:            time.Now,
	}, nil
}

func (r *AuthorizationRefresher) RefreshProviderAuthorization(ctx context.Context, authorizationID string, reason string) error {
	if !r.enabled {
		return application.NewProviderError("github_provider_disabled", false, "github scm disabled", application.ErrProviderUnavailable)
	}
	manager := newFreshUserTokenManager(
		r.cfg,
		r.http,
		r.authorizations,
		r.cipher,
		r.now,
		withFreshTokenRequestSkew(r.maintenance.ProactiveRefreshWindow.Duration()),
		withFreshTokenRefreshLeaseDuration(r.maintenance.RefreshLeaseDuration.Duration()),
	)
	_, err := manager.EnsureFreshAuthorization(ctx, EnsureFreshUserTokenInput{
		AuthorizationID: strings.TrimSpace(authorizationID),
		Provider:        domain.ProviderGitHub,
		ForceRefresh:    false,
		Reason:          strings.TrimSpace(reason),
	})
	if err != nil {
		return mapProviderError(err)
	}
	return nil
}
