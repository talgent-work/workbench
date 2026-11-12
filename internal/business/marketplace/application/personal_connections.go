package application

import (
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type PersonalConnections struct {
	authorizations    AuthorizationStore
	queries           query.ProviderAuthorizationQueryRepository
	github            GitHubPort
	tasks             MarketplaceTaskQueuePort
	authorizationPort domain.ProviderAuthorizationPort
}

func NewPersonalConnections(authorizations AuthorizationStore, queries query.ProviderAuthorizationQueryRepository, github GitHubPort,
	tasks MarketplaceTaskQueuePort, authorizationPort domain.ProviderAuthorizationPort,
) *PersonalConnections {
	return &PersonalConnections{authorizations: authorizations, queries: queries, github: github, tasks: tasks, authorizationPort: authorizationPort}
}
