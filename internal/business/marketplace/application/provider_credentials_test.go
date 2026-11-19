package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// The fake is the Provider: it renews and exchanges what it is asked to, or
// fails as told. Every decision about when to ask runs in the real code.
type fakeProviderPort struct {
	renewals, exchanges []string
	fail                error
	refuse              bool
	now                 time.Time
}

func (p *fakeProviderPort) Renew(_ context.Context, credentials domain.CredentialSet) (domain.CredentialRenewal, error) {
	p.renewals = append(p.renewals, credentials.RefreshTokenCiphertext)
	if p.fail != nil {
		return domain.CredentialRenewal{}, p.fail
	}
	if p.refuse {
		return domain.CredentialRenewal{ReconnectRequired: true}, nil
	}
	return domain.CredentialRenewal{Credentials: domain.CredentialSet{AccessTokenCiphertext: "renewed-access", RefreshTokenCiphertext: "renewed-refresh",
		ExpiresAt: p.now.Add(time.Hour), RefreshTokenExpiresAt: p.now.Add(24 * time.Hour)}}, nil
}

func (p *fakeProviderPort) ObtainTargetCredential(_ context.Context, _ domain.CredentialSet, target domain.ProviderTarget) (domain.TargetCredentialOutcome, error) {
	p.exchanges = append(p.exchanges, target.Name)
	if p.fail != nil {
		return domain.TargetCredentialOutcome{}, p.fail
	}
	return domain.TargetCredentialOutcome{Credential: domain.TargetCredential{TokenCiphertext: "for-" + target.Name, ExpiresAt: p.now.Add(time.Hour)}}, nil
}

func (p *fakeProviderPort) Revoke(context.Context, domain.CredentialSet) (bool, error) {
	return true, nil
}

type onePort struct {
	port domain.ProviderAuthorizationPort
}

func (o onePort) For(domain.Provider) (domain.ProviderAuthorizationPort, bool) { return o.port, true }

// plainCipher reads a ciphertext as its plaintext.
type plainCipher struct{}

func (plainCipher) Encrypt(plaintext string) (string, error)  { return plaintext, nil }
func (plainCipher) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

func enterpriseDeclaration(derivation domain.TargetCredentialDerivation) domain.ProviderDeclaration {
	targets := []domain.ProviderTarget{{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}}, {Name: "tickets", Hosts: []domain.HostPattern{"*.tickets.example.com"}}}
	return domain.ProviderDeclaration{Name: "example-sso", DisplayName: "Example SSO", Derivation: derivation, CredentialsExpire: true, Targets: targets}
}

func credentialsFixture(t *testing.T, derivation domain.TargetCredentialDerivation, expiresIn time.Duration) (*ProviderCredentials, *fakeAuthorizationRepository, *fakeProviderPort, string) {
	t.Helper()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	store := &fakeAuthorizationRepository{authorizations: map[string]*domain.ProviderAuthorization{}}
	auth, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{OwnerID: "user-1", Provider: "example-sso", ProviderAccountID: "sub-1", ProviderLogin: "alice",
		AccessTokenCiphertext: "initial-access", RefreshTokenCiphertext: "initial-refresh", ExpiresAt: now.Add(expiresIn)})
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), auth))
	port := &fakeProviderPort{now: now}
	credentials := NewProviderCredentials(store, staticProviders{enterpriseDeclaration(derivation)}, onePort{port}, plainCipher{},
		CredentialTiming{RequestSkew: 5 * time.Minute, ProactiveWindow: 30 * time.Minute, LeaseDuration: 30 * time.Second, Clock: func() time.Time { return now }})
	return credentials, store, port, auth.ID
}

// A shared target takes the authorization's own credential, reaches only its
// declared hosts and asks the Provider only once it nears expiry.
func TestProviderCredentials_SharedTargetsServeTheAuthorizationsCredential(t *testing.T) {
	credentials, _, port, id := credentialsFixture(t, domain.SharedTargetCredential, time.Hour)
	material, err := credentials.SupplyTarget(t.Context(), "user-1", id, "tickets")
	require.NoError(t, err)
	require.Equal(t, "initial-access", material.Token)
	require.Equal(t, []string{"*.tickets.example.com"}, material.Hosts)
	require.Empty(t, port.renewals)

	near, _, _, nearID := credentialsFixture(t, domain.SharedTargetCredential, 2*time.Minute)
	material, err = near.SupplyTarget(t.Context(), "user-1", nearID, "docs")
	require.NoError(t, err)
	require.Equal(t, "renewed-access", material.Token, "a credential within the request skew is renewed first")
}

// One authorization yields a distinct credential per target, each saved and
// reused until it nears expiry.
func TestProviderCredentials_PerTargetCredentialsAreEachTheirOwn(t *testing.T) {
	credentials, store, port, id := credentialsFixture(t, domain.PerTargetCredential, time.Hour)
	docs, err := credentials.SupplyTarget(t.Context(), "user-1", id, "docs")
	require.NoError(t, err)
	tickets, err := credentials.SupplyTarget(t.Context(), "user-1", id, "tickets")
	require.NoError(t, err)
	require.Equal(t, "for-docs", docs.Token)
	require.Equal(t, "for-tickets", tickets.Token)
	again, err := credentials.SupplyTarget(t.Context(), "user-1", id, "docs")
	require.NoError(t, err)
	require.Equal(t, docs, again)
	require.Equal(t, []string{"docs", "tickets"}, port.exchanges)
	saved, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, saved.TargetCredentials, 2)
}

func TestProviderCredentials_RefusesWhatTheDeclarationDoesNotAdmit(t *testing.T) {
	credentials, _, port, id := credentialsFixture(t, domain.SharedTargetCredential, time.Hour)
	_, err := credentials.SupplyTarget(t.Context(), "user-1", id, "github-api")
	require.ErrorIs(t, err, domain.ErrCredentialTargetInapplicable, "a target the Provider does not declare")
	_, err = credentials.SupplyTarget(t.Context(), "user-2", id, "docs")
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
	withdrawn := NewProviderCredentials(credentials.store, staticProviders{}, onePort{port}, plainCipher{}, credentials.timing)
	_, err = withdrawn.SupplyTarget(t.Context(), "user-1", id, "docs")
	require.ErrorIs(t, err, ErrProviderUnavailable, "a Provider no longer declared supplies nothing")
}

// A temporary Provider failure is recorded, releases the lease and is
// retryable; a refused grant asks the owner to reconnect.
func TestProviderCredentials_ProviderFailures(t *testing.T) {
	credentials, store, port, id := credentialsFixture(t, domain.SharedTargetCredential, time.Minute)
	port.fail = errors.New("connection reset")
	_, err := credentials.SupplyTarget(t.Context(), "user-1", id, "docs")
	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	require.True(t, providerErr.Retryable)
	saved, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderAuthorizationActive, saved.Status)
	require.Equal(t, string(ProviderErrorTransient), saved.LastFailureCode)

	port.fail, port.refuse = nil, true
	_, err = credentials.SupplyTarget(t.Context(), "user-1", id, "docs")
	require.ErrorIs(t, err, domain.ErrAuthorizationReconnectRequired)
	saved, err = store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderAuthorizationReconnectRequired, saved.Status)
}

// A caller leaving mid-renewal is no Provider failure: the lease is released
// and nothing is recorded against the authorization.
func TestProviderCredentials_CallerCancellationRecordsNoFailure(t *testing.T) {
	credentials, store, port, id := credentialsFixture(t, domain.SharedTargetCredential, time.Minute)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	port.fail = context.Canceled
	_, err := credentials.SupplyTarget(cancelled, "user-1", id, "docs")
	require.ErrorIs(t, err, context.Canceled)
	saved, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, saved.LastFailureCode)
	require.Equal(t, domain.ProviderAuthorizationActive, saved.Status)
}

// conflictingStore loses the lease to another worker, whose result it
// stores first.
type conflictingStore struct {
	*fakeAuthorizationRepository
	winner func(*domain.ProviderAuthorization)
}

func (s conflictingStore) AcquireCredentialLease(ctx context.Context, auth *domain.ProviderAuthorization, _ string, _, _ time.Time) (*domain.ProviderAuthorization, error) {
	current, err := s.Get(ctx, auth.ID)
	if err != nil {
		return nil, err
	}
	s.winner(current)
	s.save(current)
	return nil, domain.ErrConcurrentModification
}

// A concurrent renewal's result is used once it is current; otherwise the
// renewal is reported in progress, and a foreign record is never returned.
func TestProviderCredentials_ConcurrentRenewal(t *testing.T) {
	credentials, store, port, id := credentialsFixture(t, domain.SharedTargetCredential, time.Minute)
	credentials.store = conflictingStore{store, func(a *domain.ProviderAuthorization) {
		a.AccessTokenCiphertext, a.ExpiresAt = "winner-access", credentials.now().Add(time.Hour)
	}}
	material, err := credentials.SupplyTarget(t.Context(), "user-1", id, "docs")
	require.NoError(t, err)
	require.Equal(t, "winner-access", material.Token)
	require.Empty(t, port.renewals)

	credentials, store, _, id = credentialsFixture(t, domain.SharedTargetCredential, time.Minute)
	credentials.store = conflictingStore{store, func(*domain.ProviderAuthorization) {}}
	_, err = credentials.SupplyTarget(t.Context(), "user-1", id, "docs")
	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	require.Equal(t, ProviderErrorRefreshInProgress, providerErr.Code)

	credentials, store, _, id = credentialsFixture(t, domain.SharedTargetCredential, time.Minute)
	credentials.store = conflictingStore{store, func(a *domain.ProviderAuthorization) { a.OwnerID = "user-2" }}
	_, _, err = credentials.Fresh(t.Context(), id, "user-1", false)
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
}

// The renewal task renews only within the proactive window.
func TestProviderCredentials_RenewAuthorizationWithinTheWindow(t *testing.T) {
	credentials, _, port, id := credentialsFixture(t, domain.SharedTargetCredential, time.Hour)
	require.NoError(t, credentials.RenewAuthorization(t.Context(), id))
	require.Empty(t, port.renewals)
	credentials, store, port, id := credentialsFixture(t, domain.SharedTargetCredential, 20*time.Minute)
	require.NoError(t, credentials.RenewAuthorization(t.Context(), id))
	require.Equal(t, []string{"initial-refresh"}, port.renewals)
	saved, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(saved.AccessTokenCiphertext, "renewed"))

	// A target credential expiring first is due as well; renewal replaces it.
	credentials, store, port, id = credentialsFixture(t, domain.PerTargetCredential, time.Hour)
	held, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	held.TargetCredentials = map[string]domain.TargetCredential{"docs": {TokenCiphertext: "for-docs", ExpiresAt: credentials.now().Add(10 * time.Minute)}}
	store.save(held)
	require.NoError(t, credentials.RenewAuthorization(t.Context(), id))
	require.Len(t, port.renewals, 1)
	saved, err = store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, saved.TargetCredentials, "the next supply obtains it afresh")
}
