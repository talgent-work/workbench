package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// ConnectorPublication is a platform deployment operation, never a personal
// request. Without a Provider it publishes the required Work-identity entry.
type ConnectorPublication struct {
	Name          string
	Presentation  domain.CatalogPresentation
	Configuration domain.MCPConfiguration
	Provider      *domain.ProviderAccessBinding
	Evidence      *domain.ProviderCompatibility
}

// PublishConnector registers or reconfigures one catalog entry. Concurrent
// process startup converges on one stable identity per name.
func (a *Application) PublishConnector(ctx context.Context, publication ConnectorPublication) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		connector, err := a.connectors.GetByName(ctx, publication.Name)
		switch {
		case errors.Is(err, domain.ErrConnectorNotFound) && publication.Provider == nil:
			connector, err = domain.EstablishRequiredPlatformConnector(publication.Name, publication.Configuration, publication.Presentation)
		case errors.Is(err, domain.ErrConnectorNotFound):
			if publication.Evidence == nil {
				return domain.ErrInvalidConnector
			}
			connector, err = domain.RegisterProviderConnector(publication.Name, publication.Configuration, *publication.Provider, publication.Presentation, *publication.Evidence)
		case err == nil:
			var changed bool
			changed, err = connector.Reconfigure(publication.Configuration, publication.Provider, publication.Presentation, publication.Evidence)
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
