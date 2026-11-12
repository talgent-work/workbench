package github

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func (s *CredentialSupplier) SupplyServiceCredential(ctx context.Context, owner, authorizationID, target string) (credential.ServiceMaterial, error) {
	if !s.enabled {
		return credential.ServiceMaterial{}, application.ErrProviderUnavailable
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(authorizationID) == "" {
		return credential.ServiceMaterial{}, domain.ErrPermissionDenied
	}
	// These two GitHub.com contracts accept GitHub user access tokens. A custom
	// endpoint, MCP operator or matching brand name cannot extend this allowlist.
	// Contract: github/github-mcp-server/docs/host-integration.md.
	if (target != "github-api" && target != "github-remote-mcp") || strings.TrimRight(s.cfg.webBaseURL(), "/") != "https://github.com" || strings.TrimRight(s.cfg.apiBaseURL(), "/") != "https://api.github.com" {
		return credential.ServiceMaterial{}, domain.ErrCredentialTargetInapplicable
	}
	manager := newFreshUserTokenManager(s.cfg, s.http, s.authorizations, s.cipher, s.now)
	auth, token, err := manager.EnsureFreshUserToken(ctx, EnsureFreshUserTokenInput{OwnerID: owner, AuthorizationID: authorizationID, Provider: domain.ProviderGitHub, Reason: target})
	if err != nil {
		return credential.ServiceMaterial{}, mapProviderError(err)
	}
	expiresAt, err := s.runtimeExpiry(auth)
	if err != nil {
		return credential.ServiceMaterial{}, err
	}
	return credential.ServiceMaterial{Token: token, ExpiresAt: expiresAt}, nil
}
