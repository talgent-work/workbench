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
	return RegisteredApplication{Application: assembleOwnedApplication(registered, a.now(), a.secretOverlap), ClientSecret: string(secret)}, nil
}

// OwnedApplications lists the Applications the person owns.
func (a *Application) OwnedApplications(ctx context.Context, owner string) ([]query.OwnedApplication, error) {
	person, err := domain.ParseAccountID(owner)
	if err != nil {
		return nil, err
	}
	owned, err := a.applicationQueries.OwnedApplications(ctx, string(person), a.now())
	if err != nil {
		return nil, err
	}
	for i := range owned {
		owned[i].SecretOverlap = a.secretOverlap
	}
	return owned, nil
}

// OwnedApplication reads one Application the person owns; any other answers
// ErrApplicationNotFound, as an unknown one does.
func (a *Application) OwnedApplication(ctx context.Context, owner, id string) (query.OwnedApplication, error) {
	person, found, err := a.applicationFor(ctx, owner, id)
	if err != nil {
		return query.OwnedApplication{}, err
	}
	if !found.ManageableBy(person) {
		return query.OwnedApplication{}, domain.ErrApplicationNotFound
	}
	return assembleOwnedApplication(found, a.now(), a.secretOverlap), nil
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
		owner, found, err := a.applicationFor(ctx, cmd.Owner, cmd.ApplicationID)
		if err != nil {
			return err
		}
		now := a.now()
		secret, err := found.RotateSecret(owner, now, a.secretOverlap)
		if err != nil {
			return err
		}
		if err := a.applications.Save(ctx, found); err != nil {
			return err
		}
		rotated = RotatedClientSecret{Application: assembleOwnedApplication(found, now, a.secretOverlap), ClientSecret: string(secret)}
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
		owner, found, err := a.applicationFor(ctx, cmd.Owner, cmd.ApplicationID)
		if err != nil {
			return err
		}
		now := a.now()
		if err := found.RevokeSecret(owner, cmd.ClientSecretID, now); err != nil {
			return err
		}
		if err := a.applications.Save(ctx, found); err != nil {
			return err
		}
		revoked = assembleOwnedApplication(found, now, a.secretOverlap)
		return nil
	})
	return revoked, err
}

// RenameApplication names the Application its owner renames.
type RenameApplication struct {
	Owner         string
	ApplicationID string
	Name          string
}

// RenameApplication replaces the Application's Name.
func (a *Application) RenameApplication(ctx context.Context, cmd RenameApplication) (query.OwnedApplication, error) {
	var renamed query.OwnedApplication
	err := retryOnceOnConflict(func() error {
		owner, found, err := a.applicationFor(ctx, cmd.Owner, cmd.ApplicationID)
		if err != nil {
			return err
		}
		changed, err := found.Rename(owner, cmd.Name)
		if err != nil {
			return err
		}
		if changed {
			if err := a.applications.Save(ctx, found); err != nil {
				return err
			}
		}
		renamed = assembleOwnedApplication(found, a.now(), a.secretOverlap)
		return nil
	})
	return renamed, err
}

// applicationFor parses the trusted caller and loads the Application they
// name; the Application itself decides what that caller may do.
func (a *Application) applicationFor(ctx context.Context, caller, id string) (domain.AccountID, *domain.Application, error) {
	person, err := domain.ParseAccountID(caller)
	if err != nil {
		return "", nil, err
	}
	applicationID, err := domain.ParseApplicationID(id)
	if err != nil {
		return "", nil, err
	}
	found, err := a.applications.Get(ctx, applicationID)
	if err != nil {
		return "", nil, err
	}
	return person, found, nil
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
	// Sign first: a grant that ends without a token records no use, so the
	// last use an owner reads always obtained one.
	token, err := a.tokens.Sign(ctx, claims)
	if err != nil {
		return IssuedToken{}, err
	}
	if recorded {
		if err := a.applications.Save(ctx, found); err != nil {
			return IssuedToken{}, err
		}
	}
	return IssuedToken{Token: token, ExpiresAt: claims.ExpiresAt}, nil
}
