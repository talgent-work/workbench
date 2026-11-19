package github

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// TokenCipher protects the GitHub credentials held at rest.
type TokenCipher = application.CredentialCipher

// FreshCredentials keeps an authorization's own credential valid for the
// adapter's GitHub API calls; the Marketplace's ProviderCredentials serves it.
type FreshCredentials interface {
	Fresh(ctx context.Context, authorizationID, ownerID string, force bool) (*domain.ProviderAuthorization, string, error)
}

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
