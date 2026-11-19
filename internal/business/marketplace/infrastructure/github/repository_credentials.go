package github

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// SupplyRepositoryCredential presents the user token for one repository on
// this deployment's GitHub, until the token's own expiry.
func (i *CredentialSupplier) SupplyRepositoryCredential(_ context.Context, cloneURL, token string, expiresAt time.Time) (credential.MaterializedCredential, error) {
	if !i.enabled {
		return credential.MaterializedCredential{}, application.ErrProviderUnavailable
	}
	target, err := url.Parse(cloneURL)
	base, baseErr := url.Parse(i.cfg.webBaseURL())
	if err != nil || baseErr != nil || target.Scheme != "https" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || !strings.EqualFold(target.Host, base.Host) || target.Path == "" || target.Path == "/" {
		return credential.MaterializedCredential{}, domain.ErrCredentialTargetInapplicable
	}
	if strings.TrimSpace(token) == "" || (!expiresAt.IsZero() && !expiresAt.After(i.now().UTC())) {
		return credential.MaterializedCredential{}, application.ErrProviderUnavailable
	}
	return credential.MaterializedCredential{Username: "x-access-token", Password: token, ExpiresAt: expiresAt.UTC()}, nil
}
