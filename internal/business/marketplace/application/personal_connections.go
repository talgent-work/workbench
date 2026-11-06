package application

import (
	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type PersonalConnections struct {
	authorizations      AuthorizationStore
	providerQueries     ProviderConnectionQueryRepository
	observer            ProviderAccessObserver
	observations        AccessScopeObservations
	authorizer          ProviderAuthorizer
	authorizationPort   domain.ProviderAuthorizationPort
	supplier            credential.RepositoryCredentialSupplier
	serviceSupplier     credential.ServiceCredentialSupplier
	authorizationEvents query.AuthorizationEventQueryRepository
}

func NewPersonalConnections(authorizations AuthorizationStore, observer ProviderAccessObserver, observations AccessScopeObservations, authorizer ProviderAuthorizer,
	supplier credential.RepositoryCredentialSupplier, authorizationPort domain.ProviderAuthorizationPort,
	providerQueries ProviderConnectionQueryRepository, serviceSupplier credential.ServiceCredentialSupplier,
	authorizationEvents query.AuthorizationEventQueryRepository,
) *PersonalConnections {
	return &PersonalConnections{authorizations: authorizations, observer: observer, observations: observations, authorizer: authorizer, supplier: supplier,
		authorizationPort: authorizationPort, providerQueries: providerQueries, serviceSupplier: serviceSupplier,
		authorizationEvents: authorizationEvents}
}
