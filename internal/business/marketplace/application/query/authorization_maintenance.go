package query

import (
	"context"
	"time"
)

// AuthorizationMaintenanceQueryRepository selects a bounded set of identities.
// The processor loads current aggregates before deciding which work to enqueue.
// A scan never needs to project protected credentials.
type AuthorizationMaintenanceQueryRepository interface {
	ListRefreshCandidates(ctx context.Context, refreshCutoff, leaseCutoff time.Time, limit int) ([]string, error)
	// ListScopeObservationCandidates selects Active authorizations whose scope
	// was last observed before the cutoff, or never, oldest first.
	ListScopeObservationCandidates(ctx context.Context, observedBefore time.Time, limit int) ([]string, error)
}
