package application

import (
	"time"

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
