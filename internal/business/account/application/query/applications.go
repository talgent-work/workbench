package query

import (
	"context"
	"time"
)

// OwnedApplication is an Application as its Application Owner sees it,
// without any secret.
type OwnedApplication struct {
	ID           string
	Name         string
	RegisteredAt time.Time
	// Secrets are its Client Secrets that still match: the current one first,
	// then one in its rotation overlap.
	Secrets []ClientSecret
	// SecretOverlap is how long a replaced Client Secret keeps matching
	// after a rotation, the deployment's Rotation overlap.
	SecretOverlap time.Duration
}

// ClientSecretState is where a listed Client Secret stands.
type ClientSecretState int

const (
	ClientSecretCurrent ClientSecretState = iota + 1
	ClientSecretOverlapping
)

// ClientSecret describes one Client Secret without revealing it.
type ClientSecret struct {
	ID       string
	State    ClientSecretState
	Hint     string
	IssuedAt time.Time
	// LastUsedAt is zero for a secret that never obtained a token.
	LastUsedAt time.Time
	// OverlapEndsAt is zero for the current secret.
	OverlapEndsAt time.Time
}

// ApplicationQueryRepository reads Applications by their Application Owner.
type ApplicationQueryRepository interface {
	// OwnedApplications lists the active Applications owner owns, oldest
	// first, with the secrets still valid at now.
	OwnedApplications(ctx context.Context, owner string, now time.Time) ([]OwnedApplication, error)
}
