package github

import (
	"context"
	"net/url"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func (i *CredentialSupplier) SupplyRepositoryCredential(ctx context.Context, owner, authorizationID, cloneURL string) (credential.MaterializedCredential, error) {
	if !i.enabled {
		return credential.MaterializedCredential{}, application.ErrProviderUnavailable
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(authorizationID) == "" {
		return credential.MaterializedCredential{}, domain.ErrPermissionDenied
	}
	target, err := url.Parse(cloneURL)
	base, baseErr := url.Parse(i.cfg.webBaseURL())
	if err != nil || baseErr != nil || target.Scheme != "https" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || !strings.EqualFold(target.Host, base.Host) || target.Path == "" || target.Path == "/" {
		return credential.MaterializedCredential{}, domain.ErrCredentialTargetInapplicable
	}
	manager := newFreshUserTokenManager(i.cfg, i.http, i.authorizations, i.cipher, i.now)
	auth, token, err := manager.EnsureFreshUserToken(ctx, EnsureFreshUserTokenInput{OwnerID: owner, AuthorizationID: authorizationID, Provider: domain.ProviderGitHub, Reason: "work_repository"})
	if err != nil {
		return credential.MaterializedCredential{}, mapProviderError(err)
	}
	expiresAt, err := i.runtimeExpiry(auth)
	if err != nil {
		return credential.MaterializedCredential{}, err
	}
	return credential.MaterializedCredential{Username: "x-access-token", Password: token, ExpiresAt: expiresAt}, nil
}
