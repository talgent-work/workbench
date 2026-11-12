package domain

import (
	"strings"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/google/uuid"
)

type Provider string

const ProviderGitHub Provider = "github"

type ProviderAuthorizationStatus string

const (
	ProviderAuthorizationActive            ProviderAuthorizationStatus = "active"
	ProviderAuthorizationReconnectRequired ProviderAuthorizationStatus = "reconnect_required"
	ProviderAuthorizationRevoked           ProviderAuthorizationStatus = "revoked"
)

type ProviderAuthorization struct {
	ExternalAccount
	CredentialSet
	AccessScope       AccessScope
	ID                string `validate:"required"`
	OwnerID           string `validate:"required"`
	Provider          Provider
	Status            ProviderAuthorizationStatus
	LastRefreshedAt   time.Time
	LastCheckedAt     time.Time
	LastFailureCode   string
	LastFailureDetail string
	Version           int
	events            domainevent.Collection
}

type ProviderAuthorizationInput struct {
	OwnerID                string
	Provider               Provider
	ProviderAccountID      string
	ProviderLogin          string
	AccessTokenCiphertext  string
	RefreshTokenCiphertext string
	ExpiresAt              time.Time
	RefreshTokenExpiresAt  time.Time
}

func NewProviderAuthorization(input ProviderAuthorizationInput) (*ProviderAuthorization, error) {
	a := &ProviderAuthorization{
		ID:       uuid.Must(uuid.NewV7()).String(),
		OwnerID:  strings.TrimSpace(input.OwnerID),
		Provider: input.Provider,
		Status:   ProviderAuthorizationActive,

		ExternalAccount: ExternalAccount{
			ProviderAccountID: strings.TrimSpace(input.ProviderAccountID),
			ProviderLogin:     strings.TrimSpace(input.ProviderLogin),
		},

		CredentialSet: CredentialSet{
			AccessTokenCiphertext:  strings.TrimSpace(input.AccessTokenCiphertext),
			RefreshTokenCiphertext: strings.TrimSpace(input.RefreshTokenCiphertext),
			ExpiresAt:              input.ExpiresAt.UTC(),
			RefreshTokenExpiresAt:  input.RefreshTokenExpiresAt.UTC(),
		},
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}

func RehydrateProviderAuthorization(a ProviderAuthorization) (*ProviderAuthorization, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *ProviderAuthorization) Validate() error {
	if a == nil {
		return ErrInvalidRequiredField
	}
	if err := domainValidator.Struct(a); err != nil {
		return ErrInvalidRequiredField
	}
	if !validProvider(a.Provider) || !validProviderAuthorizationStatus(a.Status) {
		return ErrInvalidRequiredField
	}
	return nil
}

func (a *ProviderAuthorization) NeedsRefresh(now time.Time, skew time.Duration) bool {
	if !a.IsActive() {
		return false
	}
	if a.ExpiresAt.IsZero() {
		return false
	}
	return !a.ExpiresAt.UTC().After(now.UTC().Add(nonNegativeDuration(skew)))
}

func (a *ProviderAuthorization) CanUseToken(now time.Time, skew time.Duration) bool {
	if !a.IsActive() || strings.TrimSpace(a.AccessTokenCiphertext) == "" {
		return false
	}
	if a.ExpiresAt.IsZero() {
		return true
	}
	return a.ExpiresAt.UTC().After(now.UTC().Add(nonNegativeDuration(skew)))
}

func (a *ProviderAuthorization) ApplyRefreshSuccess(accessCiphertext, refreshCiphertext string, accessExpiresAt, refreshExpiresAt, now time.Time) error {
	accessCiphertext = strings.TrimSpace(accessCiphertext)
	refreshCiphertext = strings.TrimSpace(refreshCiphertext)
	if accessCiphertext == "" || refreshCiphertext == "" || accessExpiresAt.IsZero() || refreshExpiresAt.IsZero() {
		return ErrInvalidRequiredField
	}
	if !a.IsActive() {
		return ErrInvalidTransition
	}
	now = now.UTC()
	a.AccessTokenCiphertext = accessCiphertext
	a.RefreshTokenCiphertext = refreshCiphertext
	a.ExpiresAt = accessExpiresAt.UTC()
	a.RefreshTokenExpiresAt = refreshExpiresAt.UTC()
	a.Status = ProviderAuthorizationActive
	a.LastRefreshedAt = now
	a.LastCheckedAt = now
	a.LastFailureCode = ""
	a.LastFailureDetail = ""
	return nil
}

// MarkReconnectRequired records a confirmed inability to continue renewal;
// leaving Active is a credential change consumers must act on.
func (a *ProviderAuthorization) MarkReconnectRequired(code, detail string, now time.Time) error {
	wasActive := a.Status == ProviderAuthorizationActive
	if err := a.recordProviderFailure(ProviderAuthorizationReconnectRequired, code, detail, now); err != nil {
		return err
	}
	if wasActive {
		a.recordCredentialChange(CredentialReconnectRequired, now)
	}
	return nil
}

// MarkRevoked records confirmed revocation; repetition changes nothing.
func (a *ProviderAuthorization) MarkRevoked(now time.Time) error {
	now = now.UTC()
	if a.Status != ProviderAuthorizationRevoked {
		a.recordCredentialChange(CredentialRevoked, now)
	}
	a.Status = ProviderAuthorizationRevoked
	a.LastCheckedAt = now
	a.LastFailureCode = ""
	a.LastFailureDetail = ""
	return nil
}

func (a *ProviderAuthorization) RecordTransientFailure(code, detail string, now time.Time) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrInvalidRequiredField
	}
	now = now.UTC()
	a.LastCheckedAt = now
	a.LastFailureCode = code
	a.LastFailureDetail = strings.TrimSpace(detail)
	return nil
}

func (a *ProviderAuthorization) MarkProviderCheckSucceeded(now time.Time) error {
	if !a.IsActive() {
		return ErrInvalidTransition
	}
	now = now.UTC()
	a.LastCheckedAt = now
	a.LastFailureCode = ""
	a.LastFailureDetail = ""
	return nil
}

func (a *ProviderAuthorization) ReplaceWith(input ProviderAuthorizationInput) error {
	if strings.TrimSpace(input.OwnerID) != a.OwnerID || input.Provider != a.Provider {
		return ErrPermissionDenied
	}
	candidate := *a
	now := time.Now().UTC()
	candidate.ProviderAccountID = strings.TrimSpace(input.ProviderAccountID)
	candidate.ProviderLogin = strings.TrimSpace(input.ProviderLogin)
	candidate.AccessTokenCiphertext = strings.TrimSpace(input.AccessTokenCiphertext)
	candidate.RefreshTokenCiphertext = strings.TrimSpace(input.RefreshTokenCiphertext)
	candidate.ExpiresAt = input.ExpiresAt.UTC()
	candidate.RefreshTokenExpiresAt = input.RefreshTokenExpiresAt.UTC()
	if candidate.ProviderAccountID != a.ProviderAccountID {
		// Another external account reaches other resources; its scope is
		// observed afresh rather than inherited.
		candidate.AccessScope = AccessScope{}
	}
	candidate.Status = ProviderAuthorizationActive
	candidate.LastCheckedAt = now
	candidate.LastFailureCode = ""
	candidate.LastFailureDetail = ""
	if err := candidate.Validate(); err != nil {
		return err
	}
	*a = candidate
	a.recordCredentialChange(CredentialReauthorized, now)
	return nil
}

func validProvider(provider Provider) bool {
	return provider == ProviderGitHub
}

func validProviderAuthorizationStatus(status ProviderAuthorizationStatus) bool {
	switch status {
	case ProviderAuthorizationActive,
		ProviderAuthorizationReconnectRequired,
		ProviderAuthorizationRevoked:
		return true
	default:
		return false
	}
}

// IsActive reports whether the authorization may still supply credentials.
func (a *ProviderAuthorization) IsActive() bool {
	return a.Status == ProviderAuthorizationActive
}

func (a *ProviderAuthorization) recordProviderFailure(status ProviderAuthorizationStatus, code, detail string, now time.Time) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrInvalidRequiredField
	}
	if !validProviderAuthorizationStatus(status) || status == ProviderAuthorizationActive {
		return ErrInvalidTransition
	}
	if a.Status == ProviderAuthorizationRevoked && status != ProviderAuthorizationRevoked {
		return ErrInvalidTransition
	}
	now = now.UTC()
	a.Status = status
	a.LastCheckedAt = now
	a.LastFailureCode = code
	a.LastFailureDetail = strings.TrimSpace(detail)
	return nil
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
