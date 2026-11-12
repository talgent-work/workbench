package application

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// SignedIn is what a device receives from a sign-in or a refresh: the signed
// Identity Token and the refresh secret it alone will hold.
type SignedIn struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshSecret    string
	SessionExpiresAt time.Time
}

func assembleSignedIn(accessToken string, claims domain.IdentityClaims, secret domain.RefreshSecret, session *domain.LoginSession) SignedIn {
	return SignedIn{
		AccessToken:      accessToken,
		AccessExpiresAt:  claims.ExpiresAt,
		RefreshSecret:    string(secret),
		SessionExpiresAt: session.ExpiresAt,
	}
}

// assembleOwnedApplication is an Application as its owner sees it at now:
// its valid secrets, never their digests.
func assembleOwnedApplication(entity *domain.Application, now time.Time) query.OwnedApplication {
	valid := entity.ValidSecrets(now)
	secrets := make([]query.ClientSecret, 0, len(valid))
	for _, secret := range valid {
		state := query.ClientSecretCurrent
		if secret.State == domain.ClientSecretOverlapping {
			state = query.ClientSecretOverlapping
		}
		secrets = append(secrets, query.ClientSecret{
			ID: secret.ID, State: state, Hint: secret.Hint, IssuedAt: secret.IssuedAt,
			LastUsedAt: secret.LastUsedAt, OverlapEndsAt: secret.OverlapEndsAt,
		})
	}
	return query.OwnedApplication{ID: string(entity.ID), Name: entity.Name, RegisteredAt: entity.RegisteredAt, Secrets: secrets}
}
