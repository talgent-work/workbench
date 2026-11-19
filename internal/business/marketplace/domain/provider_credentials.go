package domain

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"
)

var (
	ErrAuthorizationReconnectRequired = errors.New("marketplace: authorization requires reconnection")
	ErrAuthorizationRevoked           = errors.New("marketplace: authorization revoked")
	ErrCredentialTargetInapplicable   = errors.New("marketplace: credential target is not applicable")
	ErrRevocationUnconfirmed          = errors.New("marketplace: provider revocation is unconfirmed")
)

type ExternalAccount struct {
	ProviderAccountID string `validate:"required"`
	ProviderLogin     string `validate:"required"`
}

// CredentialSet contains only protected material. Decryption is confined to the
// trusted Provider adapter and outbound credential supply boundary.
type CredentialSet struct {
	AccessTokenCiphertext  string `validate:"required"`
	RefreshTokenCiphertext string
	ExpiresAt              time.Time
	RefreshTokenExpiresAt  time.Time
	// TargetCredentials are the credentials obtained per Provider target, by
	// target name, for a Provider that derives one per target.
	TargetCredentials map[string]TargetCredential
}

// TargetCredential is the protected credential for one Provider target and
// its own expiry at the Provider; the zero time declares none.
type TargetCredential struct {
	TokenCiphertext string
	ExpiresAt       time.Time
}

func (c TargetCredential) usable(now time.Time, skew time.Duration) bool {
	return strings.TrimSpace(c.TokenCiphertext) != "" && (c.ExpiresAt.IsZero() || c.ExpiresAt.After(now.Add(nonNegativeDuration(skew))))
}

// TargetCredentialOutcome is what the Provider answers when asked for one
// target's credential. A Provider that rotates the refresh credential on use
// returns its replacement.
type TargetCredentialOutcome struct {
	Credential             TargetCredential
	RefreshTokenCiphertext string
	RefreshTokenExpiresAt  time.Time
	ReconnectRequired      bool
	ReasonCode             string
}

type CredentialRenewal struct {
	Credentials       CredentialSet
	ReconnectRequired bool
	ReasonCode        string
}

// ProviderAuthorizationPort reaches the authorization's Provider.
type ProviderAuthorizationPort interface {
	Renew(context.Context, CredentialSet) (CredentialRenewal, error)
	// ObtainTargetCredential exchanges the authorization for one target's
	// credential, for a Provider that derives one per target.
	ObtainTargetCredential(context.Context, CredentialSet, ProviderTarget) (TargetCredentialOutcome, error)
	Revoke(context.Context, CredentialSet) (bool, error)
}

// CredentialRequirement is supplied by the trusted authentication boundary
// from the Provider's declaration: the verified target the credential is for
// and how its credential derives. A Provider name alone names no target.
type CredentialRequirement struct {
	OwnerID    string
	Provider   Provider
	Target     ProviderTarget
	Derivation TargetCredentialDerivation
}

// NeedsCredentialWork reports whether supplying the requirement would ask
// the Provider, which then runs under the authorization's credential lease.
func (a *ProviderAuthorization) NeedsCredentialWork(requirement CredentialRequirement, now time.Time, skew time.Duration) bool {
	if requirement.Derivation == PerTargetCredential {
		return !a.TargetCredentials[requirement.Target.Name].usable(now, skew)
	}
	return !a.CanUseToken(now, skew)
}

// SupplyValidCredentials admits supply of one verified target's credential,
// deriving it from the authorization and renewing what has expired.
func (a *ProviderAuthorization) SupplyValidCredentials(ctx context.Context, requirement CredentialRequirement, now time.Time, skew time.Duration, port ProviderAuthorizationPort) (TargetCredential, error) {
	if requirement.OwnerID != a.OwnerID || requirement.OwnerID == "" {
		return TargetCredential{}, ErrPermissionDenied
	}
	if requirement.Provider != a.Provider || requirement.Target.Name == "" {
		return TargetCredential{}, ErrCredentialTargetInapplicable
	}
	switch requirement.Derivation {
	case SharedTargetCredential:
		if err := a.RenewCredentials(ctx, now, skew, false, port); err != nil {
			return TargetCredential{}, err
		}
		return TargetCredential{TokenCiphertext: a.AccessTokenCiphertext, ExpiresAt: a.ExpiresAt}, nil
	case PerTargetCredential:
		return a.obtainTargetCredential(ctx, requirement.Target, now, skew, port)
	default:
		return TargetCredential{}, ErrCredentialTargetInapplicable
	}
}

func (a *ProviderAuthorization) obtainTargetCredential(ctx context.Context, target ProviderTarget, now time.Time, skew time.Duration, port ProviderAuthorizationPort) (TargetCredential, error) {
	if err := a.CredentialSupplyError(); err != nil {
		return TargetCredential{}, err
	}
	if current := a.TargetCredentials[target.Name]; current.usable(now, skew) {
		return current, nil
	}
	if !a.refreshUsable(now) {
		_ = a.MarkReconnectRequired("authorization_reconnect_required", "refresh credential unavailable", now)
		return TargetCredential{}, ErrAuthorizationReconnectRequired
	}
	if port == nil {
		return TargetCredential{}, ErrInvalidRequiredField
	}
	outcome, err := port.ObtainTargetCredential(ctx, a.CredentialSet, target)
	if err != nil {
		return TargetCredential{}, err
	}
	if outcome.ReconnectRequired {
		_ = a.MarkReconnectRequired(reasonOr(outcome.ReasonCode), "provider requires reauthorization", now)
		return TargetCredential{}, ErrAuthorizationReconnectRequired
	}
	credential := TargetCredential{TokenCiphertext: strings.TrimSpace(outcome.Credential.TokenCiphertext), ExpiresAt: outcome.Credential.ExpiresAt.UTC()}
	if credential.TokenCiphertext == "" {
		return TargetCredential{}, ErrInvalidRequiredField
	}
	if rotated := strings.TrimSpace(outcome.RefreshTokenCiphertext); rotated != "" {
		a.RefreshTokenCiphertext, a.RefreshTokenExpiresAt = rotated, outcome.RefreshTokenExpiresAt.UTC()
	}
	// The held set is replaced, never written through: the port saw the old one.
	held := make(map[string]TargetCredential, len(a.TargetCredentials)+1)
	maps.Copy(held, a.TargetCredentials)
	held[target.Name] = credential
	a.TargetCredentials = held
	a.LastCheckedAt, a.LastFailureCode, a.LastFailureDetail = now.UTC(), "", ""
	return credential, nil
}

func (a *ProviderAuthorization) refreshUsable(now time.Time) bool {
	return strings.TrimSpace(a.RefreshTokenCiphertext) != "" && (a.RefreshTokenExpiresAt.IsZero() || a.RefreshTokenExpiresAt.After(now))
}

func reasonOr(code string) string {
	if code == "" {
		return "authorization_reconnect_required"
	}
	return code
}

func (a *ProviderAuthorization) RenewCredentials(ctx context.Context, now time.Time, skew time.Duration, force bool, port ProviderAuthorizationPort) error {
	if err := a.CredentialSupplyError(); err != nil {
		return err
	}
	if !force && a.CanUseToken(now, skew) {
		return nil
	}
	if !a.refreshUsable(now) {
		_ = a.MarkReconnectRequired("authorization_reconnect_required", "refresh credential unavailable", now)
		return ErrAuthorizationReconnectRequired
	}
	if port == nil {
		return ErrInvalidRequiredField
	}
	result, err := port.Renew(ctx, a.CredentialSet)
	if err != nil {
		return err
	}
	if result.ReconnectRequired {
		_ = a.MarkReconnectRequired(reasonOr(result.ReasonCode), "provider requires reauthorization", now)
		return ErrAuthorizationReconnectRequired
	}
	c := result.Credentials
	if err := a.ApplyRefreshSuccess(c.AccessTokenCiphertext, c.RefreshTokenCiphertext, c.ExpiresAt, c.RefreshTokenExpiresAt, now); err != nil {
		return err
	}
	// Target credentials follow the authorization's renewal: consumers act on
	// the renewal by supplying again, which obtains them afresh, so none
	// outlives the cycle that announces it.
	a.TargetCredentials = nil
	a.recordCredentialChange(CredentialRenewed, now)
	return nil
}

func (a *ProviderAuthorization) CancelAuthorization(ctx context.Context, owner string, now time.Time, port ProviderAuthorizationPort) error {
	if owner == "" || owner != a.OwnerID {
		return ErrPermissionDenied
	}
	if a.Status == ProviderAuthorizationRevoked {
		return nil
	}
	if port == nil {
		return ErrInvalidRequiredField
	}
	// Revocation needs a credential accepted by the Provider. An expiring
	// Active grant can renew internally without asking the owner to reconnect.
	if a.Status == ProviderAuthorizationActive {
		if err := a.RenewCredentials(ctx, now, 0, false, port); err != nil {
			return err
		}
	}
	confirmed, err := port.Revoke(ctx, a.CredentialSet)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrRevocationUnconfirmed
	}
	return a.MarkRevoked(now)
}

func (a *ProviderAuthorization) CredentialSupplyError() error {
	if a.Status == ProviderAuthorizationRevoked {
		return ErrAuthorizationRevoked
	}
	if a.Status == ProviderAuthorizationReconnectRequired {
		return ErrAuthorizationReconnectRequired
	}
	if a.Status != ProviderAuthorizationActive {
		return ErrInvalidTransition
	}
	return nil
}
