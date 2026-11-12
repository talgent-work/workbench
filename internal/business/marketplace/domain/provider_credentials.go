package domain

import (
	"context"
	"errors"
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
}

type CredentialRenewal struct {
	Credentials       CredentialSet
	ReconnectRequired bool
	ReasonCode        string
}

type ProviderAuthorizationPort interface {
	Renew(context.Context, CredentialSet) (CredentialRenewal, error)
	Revoke(context.Context, CredentialSet) (bool, error)
}

// CredentialRequirement is supplied by the trusted authentication boundary after
// it verifies the consumer's target and authentication contract. A Provider name
// alone must never be mapped to Applicable=true.
type CredentialRequirement struct {
	OwnerID    string
	Provider   Provider
	Applicable bool
}

func (a *ProviderAuthorization) SupplyValidCredentials(ctx context.Context, requirement CredentialRequirement, now time.Time, skew time.Duration, port ProviderAuthorizationPort) (CredentialSet, error) {
	if requirement.OwnerID != a.OwnerID || requirement.OwnerID == "" {
		return CredentialSet{}, ErrPermissionDenied
	}
	if requirement.Provider != a.Provider || !requirement.Applicable {
		return CredentialSet{}, ErrCredentialTargetInapplicable
	}
	if err := a.RenewCredentials(ctx, now, skew, false, port); err != nil {
		return CredentialSet{}, err
	}
	return a.CredentialSet, nil
}

func (a *ProviderAuthorization) RenewCredentials(ctx context.Context, now time.Time, skew time.Duration, force bool, port ProviderAuthorizationPort) error {
	if err := a.CredentialSupplyError(); err != nil {
		return err
	}
	if !force && a.CanUseToken(now, skew) {
		return nil
	}
	if strings.TrimSpace(a.RefreshTokenCiphertext) == "" || (!a.RefreshTokenExpiresAt.IsZero() && !a.RefreshTokenExpiresAt.After(now)) {
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
		code := result.ReasonCode
		if code == "" {
			code = "authorization_reconnect_required"
		}
		_ = a.MarkReconnectRequired(code, "provider requires reauthorization", now)
		return ErrAuthorizationReconnectRequired
	}
	c := result.Credentials
	if err := a.ApplyRefreshSuccess(c.AccessTokenCiphertext, c.RefreshTokenCiphertext, c.ExpiresAt, c.RefreshTokenExpiresAt, now); err != nil {
		return err
	}
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
