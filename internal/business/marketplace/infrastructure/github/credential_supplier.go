package github

import (
	"fmt"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// CredentialSupplier materializes repository credentials from the owner's
// GitHub authorization.
type CredentialSupplier struct {
	enabled bool
	cfg     Config
	fresh   FreshCredentials
	now     func() time.Time
}

func NewCredentialSupplier(cfg Config, fresh FreshCredentials) (*CredentialSupplier, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Enabled && fresh == nil {
		return nil, fmt.Errorf("github credential supplier requires fresh credentials")
	}
	return &CredentialSupplier{enabled: cfg.Enabled, cfg: cfg, fresh: fresh, now: time.Now}, nil
}

// runtimeExpiry declares the credential's own expiry at GitHub; a credential
// that does not expire declares none (the zero time).
func (i *CredentialSupplier) runtimeExpiry(auth *domain.ProviderAuthorization) (time.Time, error) {
	if auth.ExpiresAt.IsZero() {
		return time.Time{}, nil
	}
	expiresAt := auth.ExpiresAt.UTC()
	if !expiresAt.After(i.now().UTC()) {
		return time.Time{}, application.ErrProviderUnavailable
	}
	return expiresAt, nil
}
