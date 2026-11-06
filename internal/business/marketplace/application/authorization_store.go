package application

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// AuthorizationStore serializes external credential operations using the
// loaded persistence version. A successful acquisition returns a freshly loaded
// aggregate; saving that version completes the operation and releases its lease.
// A lost worker's lease expires. Lease metadata is not authorization state.
type AuthorizationStore interface {
	domain.ProviderAuthorizationRepository
	AcquireCredentialLease(context.Context, *domain.ProviderAuthorization, string, time.Time, time.Time) (*domain.ProviderAuthorization, error)
}
