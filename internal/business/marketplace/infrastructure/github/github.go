package github

import (
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// TokenCipher protects the GitHub credentials held at rest.
type TokenCipher = application.CredentialCipher

// GitHub is Marketplace's one adapter to GitHub through the platform's GitHub
// App. Each part keeps its own concern: authorization establishment, access
// scope observation and repository credential materialization.
type GitHub struct {
	*Authorizer
	*Catalog
	*CredentialSupplier
}

var _ application.GitHubPort = (*GitHub)(nil)

func NewGitHub(authorizer *Authorizer, access *Catalog, credentials *CredentialSupplier) *GitHub {
	return &GitHub{Authorizer: authorizer, Catalog: access, CredentialSupplier: credentials}
}
