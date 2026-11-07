package application

import "context"

// CredentialSupplyPort reaches Marketplace's current credential supply for a
// Work's selected Provider Authorizations: a repository's checkout credential
// and a service target's token. Each answer is current material only; Agent
// owns no grant lifecycle.
type CredentialSupplyPort interface {
	SupplyRepositoryCredential(context.Context, RepositoryCredentialRequest) (RepositoryCredentialMaterial, error)
	SupplyServiceCredential(context.Context, ServiceCredentialRequest) (ServiceCredentialMaterial, error)
}
