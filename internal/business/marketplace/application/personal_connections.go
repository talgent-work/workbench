package application

import (
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

// PersonalConnections manages owners' Provider Authorizations for every
// declared Provider: GitHub through its App, every other Provider through
// OAuth, and credential supply through ProviderCredentials.
type PersonalConnections struct {
	authorizations AuthorizationStore
	queries        query.ProviderAuthorizationQueryRepository
	providers      ProviderDeclarations
	github         GitHubPort
	oauth          OAuthProviderPort
	tasks          MarketplaceTaskQueuePort
	credentials    *ProviderCredentials
}

// ConnectionDependencies are what PersonalConnections works with; OAuth and
// Tasks may be absent.
type ConnectionDependencies struct {
	Authorizations AuthorizationStore
	Queries        query.ProviderAuthorizationQueryRepository
	Providers      ProviderDeclarations
	GitHub         GitHubPort
	OAuth          OAuthProviderPort
	Tasks          MarketplaceTaskQueuePort
	Credentials    *ProviderCredentials
}

func NewPersonalConnections(dependencies ConnectionDependencies) *PersonalConnections {
	return &PersonalConnections{authorizations: dependencies.Authorizations, queries: dependencies.Queries, providers: dependencies.Providers,
		github: dependencies.GitHub, oauth: dependencies.OAuth, tasks: dependencies.Tasks, credentials: dependencies.Credentials}
}
