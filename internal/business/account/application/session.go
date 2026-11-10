package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// ErrIdentityTokenInvalid means a presented token was not issued by Account.
var ErrIdentityTokenInvalid = errors.New("identity token invalid")

// SessionReference is what a verified Identity Token says about its session.
type SessionReference struct {
	Subject   domain.AccountID
	SessionID string
}

// IdentityTokenPort signs Identity Tokens and publishes the keys that verify
// them. Every replica signs and verifies with the same key set.
type IdentityTokenPort interface {
	Sign(ctx context.Context, claims domain.IdentityClaims) (string, error)
	// ReferenceSession verifies a token issued by Account, expired or not, and
	// names its session; ErrIdentityTokenInvalid otherwise.
	ReferenceSession(ctx context.Context, token string) (SessionReference, error)
	// PublishedKeys is the JWKS document of the active and retiring keys.
	PublishedKeys(ctx context.Context) ([]byte, error)
	// RotateKeys provisions the next key ahead of use and retires old ones.
	RotateKeys(ctx context.Context) error
}

type RefreshLoginSession struct {
	RefreshSecret string
}

// RefreshLoginSession rotates the device's refresh secret and issues a new
// token. Reuse of a replaced secret revokes that session.
func (a *Application) RefreshLoginSession(ctx context.Context, cmd RefreshLoginSession) (SignedIn, error) {
	presented, err := domain.ParseRefreshSecret(cmd.RefreshSecret)
	if err != nil {
		return SignedIn{}, err
	}
	// A concurrent refresh of the same secret commits first; the reload then
	// finds the secret replaced and treats this one as reuse.
	var signedIn SignedIn
	err = retryOnceOnConflict(func() error {
		var err error
		signedIn, err = a.refreshOnce(ctx, presented.Digest())
		return err
	})
	return signedIn, err
}

// retryOnceOnConflict repeats a Login Session change once after another
// writer changed the same session; the repeat reloads it.
func retryOnceOnConflict(change func() error) error {
	err := change()
	if errors.Is(err, domain.ErrConcurrentModification) {
		err = change()
	}
	return err
}

func (a *Application) refreshOnce(ctx context.Context, presented domain.RefreshSecretDigest) (SignedIn, error) {
	session, match, err := a.sessions.FindByRefreshSecret(ctx, presented)
	if errors.Is(err, domain.ErrLoginSessionNotFound) {
		return SignedIn{}, domain.ErrRefreshSecretUnknown
	}
	if err != nil {
		return SignedIn{}, err
	}
	account, err := a.accounts.Get(ctx, session.AccountReference)
	if err != nil {
		return SignedIn{}, err
	}
	next, err := domain.NewRefreshSecret()
	if err != nil {
		return SignedIn{}, err
	}
	now := a.now()
	claims, err := session.Refresh(match, next, account.Profile, now, a.lifetimes)
	if errors.Is(err, domain.ErrRefreshSecretReused) {
		if saveErr := a.sessions.Save(ctx, session); saveErr != nil {
			return SignedIn{}, errors.Join(err, saveErr)
		}
		return SignedIn{}, err
	}
	if err != nil {
		return SignedIn{}, err
	}
	if err := a.sessions.Save(ctx, session); err != nil {
		return SignedIn{}, err
	}
	token, err := a.tokens.Sign(ctx, claims)
	if err != nil {
		return SignedIn{}, err
	}
	return assembleSignedIn(token, claims, next, session), nil
}

// SignOut revokes the Login Session named by the device's token. Other
// sessions of the Account are untouched.
func (a *Application) SignOut(ctx context.Context, accessToken string) error {
	reference, err := a.tokens.ReferenceSession(ctx, accessToken)
	if err != nil {
		return err
	}
	return retryOnceOnConflict(func() error { return a.revoke(ctx, reference) })
}

func (a *Application) revoke(ctx context.Context, reference SessionReference) error {
	session, err := a.sessions.Get(ctx, reference.SessionID)
	if errors.Is(err, domain.ErrLoginSessionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !session.Revoke(reference.Subject, a.now()) {
		return nil
	}
	return a.sessions.Save(ctx, session)
}

const forgetBatch = 1000

// ForgetReplacedRefreshSecrets drops the replaced secrets of sessions past
// their absolute expiry, in bounded batches until none is left or ctx ends
// (Refresh rotates once needs a replaced secret only until its session
// expires).
func (a *Application) ForgetReplacedRefreshSecrets(ctx context.Context) error {
	now := a.now()
	for {
		forgotten, err := a.sessions.ForgetReplacedSecrets(ctx, now, forgetBatch)
		if err != nil || forgotten < forgetBatch {
			return err
		}
	}
}

// PublishedKeys is the JWKS the gateway validates Identity Tokens with.
func (a *Application) PublishedKeys(ctx context.Context) ([]byte, error) {
	return a.tokens.PublishedKeys(ctx)
}

// RotateSigningKeys keeps a signing key ready and retires expired ones.
func (a *Application) RotateSigningKeys(ctx context.Context) error {
	return a.tokens.RotateKeys(ctx)
}
