package credential

import (
	"context"
	"time"
)

// MaterializedCredential is transient material inside the trusted supply boundary.
// It is never part of a Work specification, saved configuration or diagnostic.
type MaterializedCredential struct {
	Username  string
	Password  string
	ExpiresAt time.Time
}

type RepositoryCredentialSupplier interface {
	SupplyRepositoryCredential(ctx context.Context, ownerUserID, authorizationID, cloneURL string) (MaterializedCredential, error)
}
