package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

var (
	// ErrSignInExpired means GitHub no longer accepts this sign-in attempt;
	// the person starts again.
	ErrSignInExpired = errors.New("github sign-in attempt expired")
	// ErrGitHubEmailMissing means the GitHub account has no verified primary
	// email to show as the Profile email.
	ErrGitHubEmailMissing = errors.New("github account has no verified primary email")
)

// GitHubIdentity is the person GitHub verified, as the login OAuth App reads
// it inside one sign-in. No GitHub token leaves the port.
type GitHubIdentity struct {
	UserID      int64
	Login       string
	DisplayName string
	AvatarURL   string
	// Email is the verified primary email; empty when GitHub has none.
	Email string
}

// GitHubIdentityPort runs the GitHub side of the authorization-code flow.
type GitHubIdentityPort interface {
	// AuthorizationURL is where the person approves the login OAuth App.
	AuthorizationURL(state, codeChallenge string) string
	// Identify exchanges the code and reads the person; ErrSignInExpired when
	// GitHub rejects the code.
	Identify(ctx context.Context, code, codeVerifier string) (GitHubIdentity, error)
}

// SignInURL names GitHub's approval page for one sign-in attempt.
func (a *Application) SignInURL(state, codeChallenge string) string {
	return a.github.AuthorizationURL(state, codeChallenge)
}

type CompleteGitHubSignIn struct {
	Code         string
	CodeVerifier string
	UserAgent    string
}

// CompleteGitHubSignIn finds or creates the Account bound to the GitHub user,
// refreshes its Profile, opens a Login Session and issues its first token.
func (a *Application) CompleteGitHubSignIn(ctx context.Context, cmd CompleteGitHubSignIn) (SignedIn, error) {
	identity, err := a.github.Identify(ctx, cmd.Code, cmd.CodeVerifier)
	if err != nil {
		return SignedIn{}, err
	}
	if identity.Email == "" {
		return SignedIn{}, ErrGitHubEmailMissing
	}
	binding, err := domain.NewGitHubBinding(identity.UserID)
	if err != nil {
		return SignedIn{}, err
	}
	profile, err := domain.NewProfile(identity.Login, identity.Email, identity.DisplayName, identity.AvatarURL)
	if err != nil {
		return SignedIn{}, err
	}
	account, err := a.signInAccount(ctx, binding, profile)
	if err != nil {
		return SignedIn{}, err
	}
	secret, err := domain.NewRefreshSecret()
	if err != nil {
		return SignedIn{}, err
	}
	now := a.now()
	session, err := domain.OpenLoginSession(account.ID, secret, cmd.UserAgent, now, a.lifetimes)
	if err != nil {
		return SignedIn{}, err
	}
	if err := a.sessions.Save(ctx, session); err != nil {
		return SignedIn{}, err
	}
	return a.issue(ctx, session, account.Profile, secret, now)
}

// Bounded so a broken collection cannot spin: a lost first-sign-in race
// reloads once and continues as Record sign-in.
const signInAttempts = 3

func (a *Application) signInAccount(ctx context.Context, binding domain.GitHubBinding, profile domain.Profile) (*domain.Account, error) {
	for range signInAttempts {
		account, err := a.accounts.FindByGitHubBinding(ctx, binding)
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			account, err = a.createAccount(ctx, binding, profile)
			if errors.Is(err, domain.ErrGitHubBindingTaken) {
				continue
			}
			return account, err
		case err != nil:
			return nil, err
		}
		if err := account.RecordSignIn(binding, profile, a.now()); err != nil {
			return nil, err
		}
		err = a.accounts.Save(ctx, account)
		if errors.Is(err, domain.ErrConcurrentModification) {
			// A concurrent sign-in of the same GitHub user recorded what GitHub
			// reported at the same moment; that record stands for this one.
			return a.accounts.FindByGitHubBinding(ctx, binding)
		}
		if err != nil {
			return nil, err
		}
		return account, nil
	}
	return nil, fmt.Errorf("account sign-in did not settle after %d attempts: %w", signInAttempts, domain.ErrConcurrentModification)
}

func (a *Application) createAccount(ctx context.Context, binding domain.GitHubBinding, profile domain.Profile) (*domain.Account, error) {
	id, err := domain.NewAccountID()
	if err != nil {
		return nil, err
	}
	account, err := domain.NewAccountFromGitHubSignIn(id, binding, profile, a.now())
	if err != nil {
		return nil, err
	}
	if err := a.accounts.Save(ctx, account); err != nil {
		return nil, err
	}
	return account, nil
}

func (a *Application) issue(ctx context.Context, session *domain.LoginSession, profile domain.Profile, secret domain.RefreshSecret, now time.Time) (SignedIn, error) {
	claims, err := session.IssueIdentityToken(profile, now, a.lifetimes)
	if err != nil {
		return SignedIn{}, err
	}
	token, err := a.tokens.Sign(ctx, claims)
	if err != nil {
		return SignedIn{}, err
	}
	return assembleSignedIn(token, claims, secret, session), nil
}
