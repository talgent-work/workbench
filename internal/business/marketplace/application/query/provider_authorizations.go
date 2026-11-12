package query

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// PersonalAuthorization is a credential-free reference to a configured record.
// Inactive records remain selectable so reauthorization can restore the same ID.
type PersonalAuthorization struct {
	ID, Provider, Status string
	SupportedTargets     []string
}

// MaintenanceIdentities is a bounded page of candidate identities, without
// credentials or aggregate state. Mutating consumers reload the selected roots.
type MaintenanceIdentities struct {
	IDs        []string
	NextCursor string
}

// ProviderAuthorizationQueryRepository reads ProviderAuthorization facts for
// selections that Get by identity does not serve: by owner, by credential
// expiry, by external account or installation, and by observation age. No
// selection projects protected credentials; mutating consumers reload the
// selected roots.
type ProviderAuthorizationQueryRepository interface {
	// ListPersonalAuthorizations lists an owner's authorizations, a person's or
	// an Application's.
	ListPersonalAuthorizations(ctx context.Context, ownerID string) ([]PersonalAuthorization, error)
	// GetProviderConnectionState is an owner's connection feedback for one
	// Provider; an owner without an authorization gets the state that asks to
	// connect.
	GetProviderConnectionState(ctx context.Context, ownerID string, provider domain.Provider) (*ProviderConnectionState, error)
	// ListRefreshCandidates selects Active authorizations whose credential
	// expires before the refresh cutoff and whose lease ended before the lease
	// cutoff, soonest first.
	ListRefreshCandidates(ctx context.Context, refreshCutoff, leaseCutoff time.Time, limit int) ([]string, error)
	// ListScopeObservationCandidates selects Active authorizations whose scope
	// was last observed before the cutoff, or never, oldest first.
	ListScopeObservationCandidates(ctx context.Context, observedBefore time.Time, limit int) ([]string, error)
	// AuthorizationIDsByAccount selects authorizations of an external account.
	AuthorizationIDsByAccount(ctx context.Context, provider domain.Provider, accountID, after string) (MaintenanceIdentities, error)
	// AuthorizationIDsByInstallation selects authorizations whose last observed
	// scope reached the Provider installation.
	AuthorizationIDsByInstallation(ctx context.Context, provider domain.Provider, installationID, after string) (MaintenanceIdentities, error)
}
