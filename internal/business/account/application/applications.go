package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// RegisterApplication names the new Application and the person registering
// it, from the trusted identity.
type RegisterApplication struct {
	Owner string
	Name  string
}

// RegisteredApplication is the new Application and its first Client Secret,
// which nothing returns again.
type RegisteredApplication struct {
	Application  query.OwnedApplication
	ClientSecret string
}

// RegisterApplication registers an Application owned by the caller and
// issues its first Client Secret.
func (a *Application) RegisterApplication(ctx context.Context, cmd RegisterApplication) (RegisteredApplication, error) {
	owner, err := domain.ParseAccountID(cmd.Owner)
	if err != nil {
		return RegisteredApplication{}, err
	}
	registered, secret, err := domain.RegisterApplication(owner, cmd.Name, a.now())
	if err != nil {
		return RegisteredApplication{}, err
	}
	if err := a.applications.Save(ctx, registered); err != nil {
		return RegisteredApplication{}, err
	}
	return RegisteredApplication{Application: assembleOwnedApplication(registered, a.now()), ClientSecret: string(secret)}, nil
}

// OwnedApplications lists the Applications the person owns.
func (a *Application) OwnedApplications(ctx context.Context, owner string) ([]query.OwnedApplication, error) {
	person, err := domain.ParseAccountID(owner)
	if err != nil {
		return nil, err
	}
	return a.applicationQueries.OwnedApplications(ctx, string(person), a.now())
}

// OwnedApplication reads one Application the person owns; any other answers
// ErrApplicationNotFound, as an unknown one does.
func (a *Application) OwnedApplication(ctx context.Context, owner, id string) (query.OwnedApplication, error) {
	person, err := domain.ParseAccountID(owner)
	if err != nil {
		return query.OwnedApplication{}, err
	}
	applicationID, err := domain.ParseApplicationID(id)
	if err != nil {
		return query.OwnedApplication{}, err
	}
	found, err := a.applications.Get(ctx, applicationID)
	if err != nil {
		return query.OwnedApplication{}, err
	}
	if !found.OwnedBy(person) || found.State != domain.ApplicationActive {
		return query.OwnedApplication{}, domain.ErrApplicationNotFound
	}
	return assembleOwnedApplication(found, a.now()), nil
}

// IssueApplicationIdentityToken is the client credentials grant: an
// Application's id and one of its Client Secrets.
type IssueApplicationIdentityToken struct {
	ClientID     string
	ClientSecret string
}

// IssueApplicationIdentityToken issues an Application's Identity Token for a
// valid Client Secret and records the secret's use. An unknown client, a
// removed Application and a secret that matches nothing all answer
// ErrClientSecretMismatch, so a caller learns nothing about which it was.
func (a *Application) IssueApplicationIdentityToken(ctx context.Context, cmd IssueApplicationIdentityToken) (IssuedToken, error) {
	id, err := domain.ParseApplicationID(cmd.ClientID)
	if err != nil {
		return IssuedToken{}, domain.ErrClientSecretMismatch
	}
	secret, err := domain.ParseClientSecretValue(cmd.ClientSecret)
	if err != nil {
		return IssuedToken{}, err
	}
	var issued IssuedToken
	// A concurrent grant for the same Application commits first; the repeat
	// reloads it and records this use on top.
	err = retryOnceOnConflict(func() error {
		var err error
		issued, err = a.issueApplicationTokenOnce(ctx, id, secret)
		return err
	})
	return issued, err
}

func (a *Application) issueApplicationTokenOnce(ctx context.Context, id domain.ApplicationID, secret domain.ClientSecretValue) (IssuedToken, error) {
	found, err := a.applications.Get(ctx, id)
	if errors.Is(err, domain.ErrApplicationNotFound) {
		return IssuedToken{}, domain.ErrClientSecretMismatch
	}
	if err != nil {
		return IssuedToken{}, err
	}
	claims, err := found.IssueIdentityToken(secret, a.now(), a.applicationTokenLifetime)
	if err != nil {
		return IssuedToken{}, err
	}
	if err := a.applications.Save(ctx, found); err != nil {
		return IssuedToken{}, err
	}
	token, err := a.tokens.Sign(ctx, claims)
	if err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{Token: token, ExpiresAt: claims.ExpiresAt}, nil
}
