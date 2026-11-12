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
	found, err := a.ownedApplication(ctx, owner, id)
	if err != nil {
		return query.OwnedApplication{}, err
	}
	return assembleOwnedApplication(found, a.now()), nil
}

// RotateClientSecret names an Application its owner rotates the secret of.
type RotateClientSecret struct {
	Owner         string
	ApplicationID string
}

// RotatedClientSecret is the Application after rotation and its new Client
// Secret, which nothing returns again.
type RotatedClientSecret struct {
	Application  query.OwnedApplication
	ClientSecret string
}

// RotateClientSecret issues the Application a new current Client Secret; the
// replaced one keeps matching for the rotation overlap.
func (a *Application) RotateClientSecret(ctx context.Context, cmd RotateClientSecret) (RotatedClientSecret, error) {
	var rotated RotatedClientSecret
	// A concurrent grant that records a use commits first; the repeat reloads.
	err := retryOnceOnConflict(func() error {
		found, err := a.ownedApplication(ctx, cmd.Owner, cmd.ApplicationID)
		if err != nil {
			return err
		}
		now := a.now()
		secret, err := found.RotateSecret(now, a.secretOverlap)
		if err != nil {
			return err
		}
		if err := a.applications.Save(ctx, found); err != nil {
			return err
		}
		rotated = RotatedClientSecret{Application: assembleOwnedApplication(found, now), ClientSecret: string(secret)}
		return nil
	})
	return rotated, err
}

// RevokeClientSecret names one Client Secret its owner revokes.
type RevokeClientSecret struct {
	Owner          string
	ApplicationID  string
	ClientSecretID string
}

// RevokeClientSecret ends one of the Application's Client Secrets at once.
func (a *Application) RevokeClientSecret(ctx context.Context, cmd RevokeClientSecret) (query.OwnedApplication, error) {
	var revoked query.OwnedApplication
	err := retryOnceOnConflict(func() error {
		found, err := a.ownedApplication(ctx, cmd.Owner, cmd.ApplicationID)
		if err != nil {
			return err
		}
		now := a.now()
		if err := found.RevokeSecret(cmd.ClientSecretID, now); err != nil {
			return err
		}
		if err := a.applications.Save(ctx, found); err != nil {
			return err
		}
		revoked = assembleOwnedApplication(found, now)
		return nil
	})
	return revoked, err
}

// ownedApplication loads an active Application the person owns; any other
// answers ErrApplicationNotFound, as an unknown one does (Owner manages).
func (a *Application) ownedApplication(ctx context.Context, owner, id string) (*domain.Application, error) {
	person, err := domain.ParseAccountID(owner)
	if err != nil {
		return nil, err
	}
	applicationID, err := domain.ParseApplicationID(id)
	if err != nil {
		return nil, err
	}
	found, err := a.applications.Get(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if !found.OwnedBy(person) || found.State != domain.ApplicationActive {
		return nil, domain.ErrApplicationNotFound
	}
	return found, nil
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
	// A concurrent grant or change of the same Application commits first; the
	// repeat reloads it, and a use another grant already recorded within
	// LastUseResolution needs no second write.
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
	claims, recorded, err := found.IssueIdentityToken(secret, a.now(), a.applicationTokenLifetime)
	if err != nil {
		return IssuedToken{}, err
	}
	if recorded {
		if err := a.applications.Save(ctx, found); err != nil {
			return IssuedToken{}, err
		}
	}
	token, err := a.tokens.Sign(ctx, claims)
	if err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{Token: token, ExpiresAt: claims.ExpiresAt}, nil
}
