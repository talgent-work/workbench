package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type ProviderAuthorizationInfo struct {
	ID, OwnerID, Provider, ProviderAccountID, ProviderLogin, Status string
}

func (a *PersonalConnections) CompletePersonalAuthorization(ctx context.Context, input CompleteProviderAuthorizationInput) (ProviderAuthorizationInfo, error) {
	if strings.TrimSpace(input.OwnerID) == "" {
		return ProviderAuthorizationInfo{}, ErrUnauthenticated
	}
	existing, err := a.authorizations.GetByOwnerAndProvider(ctx, input.OwnerID, input.Provider)
	if err != nil && !errors.Is(err, domain.ErrProviderNotFound) {
		return ProviderAuthorizationInfo{}, err
	}
	if errors.Is(err, domain.ErrProviderNotFound) {
		existing = nil
	}
	if existing != nil {
		now := time.Now().UTC()
		existing, err = a.authorizations.AcquireCredentialLease(ctx, existing, uuid.NewString(), now.Add(time.Minute), now)
		if err != nil {
			return ProviderAuthorizationInfo{}, err
		}
	}
	// Acquire before exchanging the one-use OAuth code: cancellation or renewal
	// must not race with replacement of the same retained authorization.
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	verified, operationErr := a.github.CompleteProviderAuthorization(operationCtx, input)
	if operationErr == nil {
		if verified == nil || verified.OwnerID != input.OwnerID || verified.Provider != input.Provider {
			operationErr = domain.ErrPermissionDenied
		} else {
			operationErr = verified.Validate()
		}
	}
	if operationErr != nil {
		if existing != nil {
			operationErr = a.persistCredentialOperation(ctx, existing, operationErr)
		}
		return ProviderAuthorizationInfo{}, operationErr
	}
	if existing == nil {
		existing = verified
	} else {
		operationErr = existing.ReplaceWith(domain.ProviderAuthorizationInput{
			OwnerID: verified.OwnerID, Provider: verified.Provider,
			ProviderAccountID: verified.ProviderAccountID, ProviderLogin: verified.ProviderLogin,
			AccessTokenCiphertext:  verified.AccessTokenCiphertext,
			RefreshTokenCiphertext: verified.RefreshTokenCiphertext, ExpiresAt: verified.ExpiresAt,
			RefreshTokenExpiresAt: verified.RefreshTokenExpiresAt,
		})
	}
	if err := a.persistCredentialOperation(ctx, existing, operationErr); err != nil {
		return ProviderAuthorizationInfo{}, err
	}
	a.observeAfterAuthorization(ctx, existing.ID)
	return providerAuthorizationInfo(existing), nil
}

// External token rotation survives caller cancellation. This bounded final
// write also releases the lease; a conflicting later version is never replaced.
func (a *PersonalConnections) persistCredentialOperation(ctx context.Context, auth *domain.ProviderAuthorization, operationErr error) error {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := a.authorizations.Save(saveCtx, auth); err != nil {
		return errors.Join(operationErr, err)
	}
	return operationErr
}
