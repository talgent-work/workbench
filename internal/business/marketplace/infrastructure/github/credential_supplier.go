package github

import (
	"time"
)

// CredentialSupplier materializes repository credentials from the owner's
// GitHub user token.
type CredentialSupplier struct {
	enabled bool
	cfg     Config
	now     func() time.Time
}

func NewCredentialSupplier(cfg Config) (*CredentialSupplier, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &CredentialSupplier{enabled: cfg.Enabled, cfg: cfg, now: time.Now}, nil
}
