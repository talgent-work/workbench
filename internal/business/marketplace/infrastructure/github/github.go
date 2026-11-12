package github

import "github.com/talgent-work/workbench/internal/business/marketplace/application"

// GitHub is Marketplace's one adapter to GitHub through the platform's GitHub
// App. Each part keeps its own concern: authorization establishment, access
// scope observation, credential renewal and credential materialization.
type GitHub struct {
	*Authorizer
	*Catalog
	*AuthorizationRefresher
	*CredentialSupplier
}

var _ application.GitHubPort = (*GitHub)(nil)

func NewGitHub(authorizer *Authorizer, access *Catalog, refresher *AuthorizationRefresher, credentials *CredentialSupplier) *GitHub {
	return &GitHub{Authorizer: authorizer, Catalog: access, AuthorizationRefresher: refresher, CredentialSupplier: credentials}
}
