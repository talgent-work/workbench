package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// MaintenanceIdentities is a bounded page of candidate identities, without
// credentials or aggregate state. Mutating consumers reload the selected roots.
type MaintenanceIdentities struct {
	IDs        []string
	NextCursor string
}

type AuthorizationEventQueryRepository interface {
	AuthorizationIDsByAccount(ctx context.Context, provider domain.Provider, accountID, after string) (MaintenanceIdentities, error)
	// AuthorizationIDsByInstallation selects authorizations whose last observed
	// scope reached the Provider installation.
	AuthorizationIDsByInstallation(ctx context.Context, provider domain.Provider, installationID, after string) (MaintenanceIdentities, error)
}
