package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const (
	ProviderErrorReconnectRequired    ProviderErrorCode = "reconnect_required"
	ProviderErrorAuthorizationRevoked ProviderErrorCode = "authorization_revoked"
	ProviderErrorTransient            ProviderErrorCode = "provider_transient"
	ProviderErrorRefreshInProgress    ProviderErrorCode = "refresh_in_progress"
	ProviderErrorCredentialFailed     ProviderErrorCode = "credential_failed"
)

// CredentialTiming bounds credential work: a credential expiring within
// RequestSkew is renewed before supply, the renewal task renews within
// ProactiveWindow, and one Provider exchange holds the lease LeaseDuration.
type CredentialTiming struct {
	RequestSkew, ProactiveWindow, LeaseDuration time.Duration
	// Clock is the current time; nil is the system clock.
	Clock func() time.Time
}

// ProviderCredentials keeps every Provider's authorizations' credentials
// valid and supplies their target credentials. It asks a Provider only under
// the authorization's credential lease, so concurrent supply, renewal and
// cancellation of one authorization never race at the Provider.
type ProviderCredentials struct {
	store     AuthorizationStore
	providers ProviderDeclarations
	ports     ProviderAuthorizationPorts
	cipher    CredentialCipher
	timing    CredentialTiming
	now       func() time.Time
}

func NewProviderCredentials(store AuthorizationStore, providers ProviderDeclarations, ports ProviderAuthorizationPorts, cipher CredentialCipher, timing CredentialTiming) *ProviderCredentials {
	now := timing.Clock
	if now == nil {
		now = time.Now
	}
	return &ProviderCredentials{store: store, providers: providers, ports: ports, cipher: cipher, timing: timing, now: now}
}

// Fresh returns an owner's authorization with its own credential valid
// beyond the request skew, renewing it first when needed or forced, and the
// credential's plaintext for the Provider's own adapter.
func (c *ProviderCredentials) Fresh(ctx context.Context, authorizationID, ownerID string, force bool) (*domain.ProviderAuthorization, string, error) {
	auth, err := c.load(ctx, authorizationID, ownerID)
	if err != nil {
		return nil, "", err
	}
	needs := func(a *domain.ProviderAuthorization) bool {
		return force || !a.CanUseToken(c.now().UTC(), c.timing.RequestSkew)
	}
	auth, err = c.underLease(ctx, auth, needs, func(ctx context.Context, a *domain.ProviderAuthorization, port domain.ProviderAuthorizationPort) error {
		return a.RenewCredentials(ctx, c.now().UTC(), c.timing.RequestSkew, true, port)
	})
	if err != nil {
		return nil, "", err
	}
	token, err := c.decrypt(auth.AccessTokenCiphertext)
	return auth, token, err
}

// RenewAuthorization renews an Active authorization whose credential expires
// within the proactive window; any other authorization is left alone.
func (c *ProviderCredentials) RenewAuthorization(ctx context.Context, authorizationID string) error {
	auth, err := c.load(ctx, authorizationID, "")
	if err != nil || !auth.IsActive() {
		return err
	}
	needs := func(a *domain.ProviderAuthorization) bool {
		return a.NeedsRefresh(c.now().UTC(), c.timing.ProactiveWindow)
	}
	_, err = c.underLease(ctx, auth, needs, func(ctx context.Context, a *domain.ProviderAuthorization, port domain.ProviderAuthorizationPort) error {
		return a.RenewCredentials(ctx, c.now().UTC(), c.timing.ProactiveWindow, true, port)
	})
	return err
}

// SupplyTarget supplies the credential of one target the authorization's
// Provider declares, with the hosts it may reach.
func (c *ProviderCredentials) SupplyTarget(ctx context.Context, ownerID, authorizationID, targetName string) (credential.ServiceMaterial, error) {
	auth, err := c.load(ctx, authorizationID, ownerID)
	if err != nil {
		return credential.ServiceMaterial{}, err
	}
	declaration, ok := c.providers.Provider(auth.Provider)
	if !ok {
		return credential.ServiceMaterial{}, ErrProviderUnavailable
	}
	target, ok := declaration.Target(targetName)
	if !ok {
		return credential.ServiceMaterial{}, domain.ErrCredentialTargetInapplicable
	}
	requirement := domain.CredentialRequirement{OwnerID: ownerID, Provider: auth.Provider, Target: target, Derivation: declaration.Derivation}
	var supplied domain.TargetCredential
	needs := func(a *domain.ProviderAuthorization) bool {
		return a.NeedsCredentialWork(requirement, c.now().UTC(), c.timing.RequestSkew)
	}
	auth, err = c.underLease(ctx, auth, needs, func(ctx context.Context, a *domain.ProviderAuthorization, port domain.ProviderAuthorizationPort) error {
		supplied, err = a.SupplyValidCredentials(ctx, requirement, c.now().UTC(), c.timing.RequestSkew, port)
		return err
	})
	if err != nil {
		return credential.ServiceMaterial{}, err
	}
	if supplied.TokenCiphertext == "" {
		// Nothing needed renewal: the retained credential is current.
		if supplied, err = auth.SupplyValidCredentials(ctx, requirement, c.now().UTC(), c.timing.RequestSkew, nil); err != nil {
			return credential.ServiceMaterial{}, authorizationError(err)
		}
	}
	if !supplied.ExpiresAt.IsZero() && !supplied.ExpiresAt.After(c.now().UTC()) {
		return credential.ServiceMaterial{}, ErrProviderUnavailable
	}
	token, err := c.decrypt(supplied.TokenCiphertext)
	if err != nil {
		return credential.ServiceMaterial{}, err
	}
	hosts := make([]string, len(target.Hosts))
	for i, host := range target.Hosts {
		hosts[i] = string(host)
	}
	paths := make([]string, len(target.Paths))
	for i, p := range target.Paths {
		paths[i] = string(p)
	}
	return credential.ServiceMaterial{Token: token, ExpiresAt: supplied.ExpiresAt, Hosts: hosts, Paths: paths, Methods: slices.Clone(target.Methods)}, nil
}

// port is the adapter of the authorization's declared Provider.
func (c *ProviderCredentials) port(provider domain.Provider) (domain.ProviderAuthorizationPort, error) {
	if c.ports == nil {
		return nil, ErrProviderUnavailable
	}
	port, ok := c.ports.For(provider)
	if !ok {
		return nil, ErrProviderUnavailable
	}
	return port, nil
}

func (c *ProviderCredentials) load(ctx context.Context, authorizationID, ownerID string) (*domain.ProviderAuthorization, error) {
	if strings.TrimSpace(authorizationID) == "" {
		return nil, domain.ErrInvalidRequiredField
	}
	auth, err := c.store.Get(ctx, authorizationID)
	if errors.Is(err, domain.ErrProviderNotFound) {
		return nil, NewProviderError(ProviderErrorReconnectRequired, false, "authorization missing", ErrProviderUnavailable)
	}
	if err != nil {
		return nil, NewProviderError(ProviderErrorTransient, true, "load authorization", err)
	}
	if ownerID != "" && auth.OwnerID != ownerID {
		return nil, domain.ErrPermissionDenied
	}
	return auth, authorizationError(auth.CredentialSupplyError())
}

// underLease runs the Provider operation when the authorization needs it,
// under its credential lease. A concurrent holder's result is used once it
// satisfies the need; otherwise the refresh is reported in progress.
func (c *ProviderCredentials) underLease(ctx context.Context, auth *domain.ProviderAuthorization, needs func(*domain.ProviderAuthorization) bool, operation leasedOperation) (*domain.ProviderAuthorization, error) {
	if !needs(auth) {
		return auth, nil
	}
	leased, err := c.withLease(ctx, auth, operation, func(leased *domain.ProviderAuthorization, operationErr error) error {
		switch {
		case errors.Is(operationErr, domain.ErrAuthorizationReconnectRequired), errors.Is(operationErr, domain.ErrAuthorizationRevoked),
			errors.Is(operationErr, domain.ErrCredentialTargetInapplicable), errors.Is(operationErr, domain.ErrPermissionDenied):
			return operationErr
		case ctx.Err() != nil:
			// The caller left; the Provider did not fail. The lease is released
			// with the authorization unchanged.
			return NewProviderError(ProviderErrorTransient, true, "credential renewal cancelled", ctx.Err())
		}
		if err := leased.RecordTransientFailure(string(ProviderErrorTransient), "Provider credential renewal unavailable", c.now().UTC()); err != nil {
			return err
		}
		return NewProviderError(ProviderErrorTransient, true, "Provider credential renewal unavailable", operationErr)
	})
	if errors.Is(err, errLeaseHeld) {
		return c.afterConcurrentOperation(ctx, auth, needs)
	}
	return leased, err
}

// Revoke cancels the owner's authorization at its Provider under the same
// credential lease as renewal and supply; an unconfirmed revocation keeps the
// authorization as it was.
func (c *ProviderCredentials) Revoke(ctx context.Context, ownerID string, auth *domain.ProviderAuthorization) (*domain.ProviderAuthorization, error) {
	leased, err := c.withLease(ctx, auth, func(ctx context.Context, a *domain.ProviderAuthorization, port domain.ProviderAuthorizationPort) error {
		return a.CancelAuthorization(ctx, ownerID, c.now().UTC(), port)
	}, func(_ *domain.ProviderAuthorization, operationErr error) error { return operationErr })
	if errors.Is(err, errLeaseHeld) {
		return nil, NewProviderError(ProviderErrorRefreshInProgress, true, "a credential operation is in progress", domain.ErrConcurrentModification)
	}
	return leased, err
}

// leasedOperation is one Provider operation on a leased authorization.
type leasedOperation func(context.Context, *domain.ProviderAuthorization, domain.ProviderAuthorizationPort) error

// errLeaseHeld is another worker holding the authorization's credential lease.
var errLeaseHeld = errors.New("marketplace: credential lease held")

// withLease runs one Provider operation on the authorization under its
// credential lease, bounded so it ends before the lease does, and saves the
// result, which releases the lease. failure decides what a failed operation
// records and returns.
func (c *ProviderCredentials) withLease(ctx context.Context, auth *domain.ProviderAuthorization, operation leasedOperation,
	failure func(*domain.ProviderAuthorization, error) error) (*domain.ProviderAuthorization, error) {
	port, err := c.port(auth.Provider)
	if err != nil {
		return nil, err
	}
	now := c.now().UTC()
	lease := c.leaseDuration()
	leased, err := c.store.AcquireCredentialLease(ctx, auth, uuid.NewString(), now.Add(lease), now)
	if errors.Is(err, domain.ErrConcurrentModification) {
		return nil, errLeaseHeld
	}
	if err != nil {
		return nil, NewProviderError(ProviderErrorTransient, true, "acquire credential lease", err)
	}
	operationCtx, cancel := context.WithTimeout(ctx, lease-leaseSaveWindow)
	operationErr := operation(operationCtx, leased, port)
	cancel()
	if operationErr != nil {
		if operationErr = failure(leased, operationErr); operationErr == nil {
			operationErr = ErrProviderUnavailable
		}
	}
	// The final write also releases the lease and survives caller cancellation.
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), leaseSaveWindow)
	defer cancelSave()
	if err := c.store.Save(saveCtx, leased); err != nil {
		return nil, errors.Join(authorizationError(operationErr), NewProviderError(ProviderErrorCredentialFailed, false, "persist credentials", err))
	}
	if operationErr != nil {
		return nil, authorizationError(operationErr)
	}
	return leased, nil
}

func (c *ProviderCredentials) afterConcurrentOperation(ctx context.Context, stale *domain.ProviderAuthorization, needs func(*domain.ProviderAuthorization) bool) (*domain.ProviderAuthorization, error) {
	reloaded, err := c.store.Get(ctx, stale.ID)
	if err != nil {
		return nil, NewProviderError(ProviderErrorRefreshInProgress, true, "refresh in progress", err)
	}
	if reloaded.OwnerID != stale.OwnerID || reloaded.Provider != stale.Provider {
		return nil, domain.ErrPermissionDenied
	}
	if err := authorizationError(reloaded.CredentialSupplyError()); err != nil {
		return nil, err
	}
	if !needs(reloaded) {
		return reloaded, nil
	}
	return nil, NewProviderError(ProviderErrorRefreshInProgress, true, "refresh in progress", domain.ErrConcurrentModification)
}

// leaseSaveWindow is the part of a lease kept for saving the operation's
// result.
const leaseSaveWindow = 5 * time.Second

// leaseDuration is the configured lease, never shorter than an operation
// needs beside its save window.
func (c *ProviderCredentials) leaseDuration() time.Duration {
	return max(c.timing.LeaseDuration, 30*time.Second)
}

func (c *ProviderCredentials) decrypt(ciphertext string) (string, error) {
	if c.cipher == nil {
		return "", ErrProviderUnavailable
	}
	token, err := c.cipher.Decrypt(ciphertext)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", NewProviderError(ProviderErrorCredentialFailed, false, "unable to decrypt credential", err)
	}
	return token, nil
}

// authorizationError reports an authorization that supplies nothing until
// its owner acts, keeping the domain reason for callers.
func authorizationError(err error) error {
	var providerErr *ProviderError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &providerErr):
		return err
	case errors.Is(err, domain.ErrAuthorizationReconnectRequired):
		return NewProviderError(ProviderErrorReconnectRequired, false, "authorization requires reconnection", err)
	case errors.Is(err, domain.ErrAuthorizationRevoked):
		return NewProviderError(ProviderErrorAuthorizationRevoked, false, "authorization revoked", err)
	default:
		return err
	}
}
