package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// PlatformConnector is a Platform Connector the deployment configuration
// declares, never a personal request. Without a Provider it is the required
// Work-identity entry.
type PlatformConnector struct {
	Name          string
	Presentation  domain.CatalogPresentation
	Configuration domain.MCPConfiguration
	Provider      *domain.ProviderAccessBinding
}

// compatibility is the evidence for a Provider Connector's configuration: the
// target its declared Provider declares under the binding's name.
func (a *Application) compatibility(publication PlatformConnector) (*domain.ProviderCompatibility, error) {
	if publication.Provider == nil {
		return nil, nil
	}
	if a.PersonalConnections == nil || a.providers == nil {
		return nil, ErrProviderUnavailable
	}
	declaration, ok := a.providers.Provider(domain.Provider(publication.Provider.Provider))
	if !ok {
		return nil, ErrProviderUnavailable
	}
	target, ok := declaration.Target(publication.Provider.Target)
	if !ok {
		return nil, domain.ErrCredentialTargetInapplicable
	}
	return &domain.ProviderCompatibility{Configuration: publication.Configuration.Clone(), Binding: *publication.Provider, Target: target}, nil
}

// targetCredentialsExpire reports whether a declared Provider's target
// credentials expire; an undeclared one reports false.
func (a *Application) targetCredentialsExpire(provider string) bool {
	if a.PersonalConnections == nil || a.providers == nil {
		return false
	}
	declaration, ok := a.providers.Provider(domain.Provider(provider))
	return ok && declaration.CredentialsExpire
}

// EstablishPlatformConnector establishes or reconfigures one Platform
// Connector. Concurrent process startup converges on one stable identity per
// name.
func (a *Application) EstablishPlatformConnector(ctx context.Context, publication PlatformConnector) error {
	evidence, err := a.compatibility(publication)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		connector, err := a.connectors.GetPlatform(ctx, publication.Name)
		switch {
		case errors.Is(err, domain.ErrConnectorNotFound) && publication.Provider == nil:
			connector, err = domain.EstablishRequiredPlatformConnector(publication.Name, publication.Configuration, publication.Presentation)
		case errors.Is(err, domain.ErrConnectorNotFound):
			connector, err = domain.EstablishPlatformProviderConnector(publication.Name, publication.Configuration, *publication.Provider, publication.Presentation, *evidence)
		case err == nil:
			var changed bool
			changed, err = connector.Reconfigure(publication.Configuration, publication.Provider, publication.Presentation, evidence)
			if err == nil && !changed {
				return nil
			}
		}
		if err != nil {
			return err
		}
		if err = a.connectors.Save(ctx, connector); !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
	}
	return domain.ErrConcurrentModification
}

// annotateProviderExpiry adds each Provider Connector's declared target
// credential expiry, which the deployment declares and the catalog keeps not.
func (a *Application) annotateProviderExpiry(catalog []query.CatalogConnector) {
	for i := range catalog {
		if provider := catalog[i].Provider; provider != nil {
			provider.TargetCredentialsExpire = a.targetCredentialsExpire(provider.Provider)
		}
	}
}
