package github

import (
	"fmt"
	"net/http"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

var _ credential.RepositoryCredentialSupplier = (*CredentialSupplier)(nil)

type CredentialSupplier struct {
	enabled        bool
	cfg            Config
	http           *http.Client
	authorizations application.AuthorizationStore
	cipher         TokenCipher
	now            func() time.Time
}

func NewCredentialSupplier(cfg Config, authorizations application.AuthorizationStore, cipher TokenCipher) (*CredentialSupplier, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, err
	}
	if enabled {
		if authorizations == nil {
			return nil, fmt.Errorf("github credential supplier requires provider authorization repository")
		}
		if cipher == nil {
			return nil, fmt.Errorf("github credential supplier requires token cipher")
		}
	}
	return &CredentialSupplier{enabled: enabled, cfg: cfg, http: http.DefaultClient, authorizations: authorizations, cipher: cipher, now: time.Now}, nil
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
