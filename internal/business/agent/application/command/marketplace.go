package command

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// MarketplacePort reaches Marketplace's contract under a trusted owner scope:
// what it offers a Work's start (the Connector catalog and personal
// selection, Plugin release resolution, Provider Authorizations and the
// repositories reachable under them) and the current credential supply for a
// Work's selected Provider Authorizations. Startup never changes personal
// installations, the catalog never supplies credentials, and each supplied
// credential is current material only: Agent owns no grant lifecycle.
type MarketplacePort interface {
	ReadConnectorSelection(context.Context, string) (ConnectorSelection, error)
	ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error)
	ResolvePlugin(context.Context, string, string) (domain.PluginRelease, error)
	ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error)
	ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error)
	// SupplyRepositoryCredential supplies a repository's checkout credential.
	SupplyRepositoryCredential(context.Context, RepositoryCredentialRequest) (RepositoryCredentialMaterial, error)
	// SupplyServiceCredential supplies a service target's token; a refusal
	// Marketplace explains is a *ServiceCredentialUnavailable.
	SupplyServiceCredential(context.Context, ServiceCredentialRequest) (ServiceCredentialMaterial, error)
}

// ConnectorSelection is Marketplace's catalog with the delegated owner's
// enabled installation identities: a person's or an Application's.
type ConnectorSelection struct {
	Catalog           []domain.CatalogConnector
	EnabledConnectors []string
}

type RepositoryCredentialRequest struct {
	OwnerUserID, AuthorizationID, RepositoryID, CloneURL string
}

type RepositoryCredentialMaterial struct {
	Username, Password string
	ExpiresAt          time.Time
}

type ServiceCredentialRequest struct{ OwnerUserID, AuthorizationID, Target string }

type ServiceCredentialMaterial struct {
	Token     string
	ExpiresAt time.Time
}

// ServiceCredentialUnavailable is Marketplace's refusal to supply a credential.
// Temporary means Marketplace confirmed nothing about the authorization, as
// when it or the Provider could not be reached; any other refusal is
// confirmed.
type ServiceCredentialUnavailable struct {
	Reason    string
	Temporary bool
}

func (e *ServiceCredentialUnavailable) Error() string { return "agent: service credential unavailable" }
