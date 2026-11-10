package domain

import "time"

// Principal is the kind of subject an Identity Token speaks for.
type Principal string

const (
	// PrincipalUser speaks for a person by their Account.
	PrincipalUser Principal = "user"
	// PrincipalAgent speaks for a Work's Agent by its Agent Account.
	PrincipalAgent Principal = "agent"
)

// IdentityClaims are what an Identity Token states; signing them is a
// realization step outside the Aggregates. A user's claims carry the Profile
// and Login Session; an agent's carry its Work's fixed facts.
type IdentityClaims struct {
	Principal Principal
	// Subject is the Account id of a user, the Work id of an agent.
	Subject string
	// Profile and SessionID are a user's.
	Profile   Profile
	SessionID string
	// Work is an agent's.
	Work      WorkFacts
	IssuedAt  time.Time
	ExpiresAt time.Time
}
