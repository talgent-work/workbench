package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// ConnectorAuthoring is an owner's description of a Connector to add.
type ConnectorAuthoring struct {
	Owner         domain.ConnectorOwner
	Name          string
	Configuration domain.MCPConfiguration
	Presentation  domain.CatalogPresentation
	// Provider is the declared Provider it signs in with; empty for none.
	Provider string
}

// ConnectorChange replaces an owned Connector's changeable facts: the
// address of a remote server or the command of a local one, its description
// and category.
type ConnectorChange struct {
	OwnerID, ConnectorID string
	Version              int
	URL, Command         string
	Arguments            []string
	Description          string
	Category             string
}

// ConnectorIconChange replaces an owned Connector's icon; a nil Icon removes it.
type ConnectorIconChange struct {
	OwnerID, ConnectorID string
	Version              int
	Icon                 *domain.ConnectorIcon
}

// ConnectorCommand names an owned Connector at the version its owner read.
type ConnectorCommand struct {
	OwnerID, ConnectorID string
	Version              int
}

// AddConnector adds a Private Connector for its owner; adding never installs.
func (a *Application) AddConnector(ctx context.Context, authoring ConnectorAuthoring) (ConnectorDetail, error) {
	if strings.TrimSpace(authoring.Owner.ID) == "" {
		return ConnectorDetail{}, ErrUnauthenticated
	}
	addition := domain.ConnectorAddition{Name: authoring.Name, Configuration: authoring.Configuration, Presentation: authoring.Presentation}
	if authoring.Provider != "" {
		declaration, err := a.declaredProvider(authoring.Provider)
		if err != nil {
			return ConnectorDetail{}, err
		}
		addition.Provider = &declaration
	}
	connector, err := domain.AddConnector(authoring.Owner, addition, a.platformNames)
	if err != nil {
		return ConnectorDetail{}, err
	}
	if err := a.connectors.Save(ctx, connector); err != nil {
		return ConnectorDetail{}, err
	}
	return a.GetConnector(ctx, authoring.Owner.ID, connector.ID)
}

// ChangeConnector replaces an owned Connector's connection, description and
// category; changes reach Works started afterwards.
func (a *Application) ChangeConnector(ctx context.Context, change ConnectorChange) (ConnectorDetail, error) {
	return a.changeConnector(ctx, ConnectorCommand{OwnerID: change.OwnerID, ConnectorID: change.ConnectorID, Version: change.Version}, true, func(connector *domain.Connector, provider *domain.ProviderDeclaration) error {
		presentation := connector.Presentation
		presentation.Description, presentation.Category = change.Description, change.Category
		configuration := domain.MCPConfiguration{Type: "http", URL: change.URL}
		if change.URL == "" {
			configuration = domain.MCPConfiguration{Type: "stdio", Command: change.Command, Arguments: change.Arguments}
		}
		return connector.Change(change.OwnerID, configuration, presentation, provider)
	})
}

// ChangeConnectorIcon replaces or removes an owned Connector's icon.
func (a *Application) ChangeConnectorIcon(ctx context.Context, change ConnectorIconChange) (ConnectorDetail, error) {
	return a.changeConnector(ctx, ConnectorCommand{OwnerID: change.OwnerID, ConnectorID: change.ConnectorID, Version: change.Version}, true, func(connector *domain.Connector, provider *domain.ProviderDeclaration) error {
		presentation := connector.Presentation
		presentation.Icon = change.Icon
		return connector.Change(change.OwnerID, connector.Configuration, presentation, provider)
	})
}

// PublishConnector offers an owned Private Connector to every owner.
func (a *Application) PublishConnector(ctx context.Context, command ConnectorCommand) (ConnectorDetail, error) {
	return a.changeConnector(ctx, command, false, func(connector *domain.Connector, _ *domain.ProviderDeclaration) error {
		return connector.Publish(command.OwnerID, time.Now())
	})
}

// WithdrawConnector ends the offer of a Connector its publisher published.
func (a *Application) WithdrawConnector(ctx context.Context, command ConnectorCommand) (ConnectorDetail, error) {
	return a.changeConnector(ctx, command, false, func(connector *domain.Connector, _ *domain.ProviderDeclaration) error {
		_, err := connector.Withdraw(domain.WithdrawingAuthority{Owner: command.OwnerID})
		return err
	})
}

// WithdrawConnectorAsOperator ends the offer of any Published Connector an
// owner published, on the platform operator's command through the private
// API. Repeating it succeeds.
func (a *Application) WithdrawConnectorAsOperator(ctx context.Context, id string) (string, error) {
	connector, err := a.connectors.Get(ctx, id)
	if err != nil {
		return "", err
	}
	changed, err := connector.Withdraw(domain.WithdrawingAuthority{Operator: true})
	if err != nil || !changed {
		return connector.Name, err
	}
	return connector.Name, a.connectors.Save(ctx, connector)
}

// DeleteConnector ends an owned Private Connector's existence; the Policy
// Remove deleted Connector's installation follows in process.
func (a *Application) DeleteConnector(ctx context.Context, command ConnectorCommand) error {
	connector, err := a.ownedConnector(ctx, command)
	if err != nil {
		return err
	}
	if err := connector.Delete(command.OwnerID, time.Now()); err != nil {
		return err
	}
	if err := a.connectors.Delete(ctx, connector); err != nil {
		return err
	}
	for _, event := range connector.Events() {
		if deleted, ok := event.(domain.PrivateConnectorDeleted); ok {
			a.removeDeletedConnectorInstallation(ctx, deleted)
		}
	}
	return nil
}

// removeDeletedConnectorInstallation realizes the Policy Remove deleted
// Connector's installation. If it cannot complete, the installation names a
// Connector that no longer exists, which no new Work selects, so the failure
// is only reported.
func (a *Application) removeDeletedConnectorInstallation(ctx context.Context, deleted domain.PrivateConnectorDeleted) {
	installation, err := a.installedConnectors.Get(ctx, deleted.OwnerID, deleted.ConnectorID)
	if errors.Is(err, domain.ErrConnectorInstallationNotFound) {
		return
	}
	if err == nil {
		installation.Uninstall()
		err = a.installedConnectors.Delete(ctx, installation)
	}
	if err != nil {
		a.logger.WarnContext(ctx, "removing a deleted Connector's installation failed", slog.String("connector_id", deleted.ConnectorID), slog.Any("error", err))
	}
}

// GetConnectorIcon reads the icon of a Connector the owner may see.
func (a *Application) GetConnectorIcon(ctx context.Context, owner, id string) (*domain.ConnectorIcon, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, ErrUnauthenticated
	}
	connector, err := a.connectors.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !connector.InCatalogOf(owner) && !connector.Owner.Is(owner) {
		if _, err := a.installedConnectors.Get(ctx, owner, id); err != nil {
			return nil, domain.ErrConnectorNotFound
		}
	}
	return connector.Presentation.Icon, nil
}

// changeConnector applies one owner command to the Connector at the version
// the owner read. A command that changes the connection or presentation gets
// its declared Provider's current declaration; publishing and withdrawing
// need none, so a Provider the deployment dropped never blocks them.
func (a *Application) changeConnector(ctx context.Context, command ConnectorCommand, withProvider bool, change func(*domain.Connector, *domain.ProviderDeclaration) error) (ConnectorDetail, error) {
	connector, err := a.ownedConnector(ctx, command)
	if err != nil {
		return ConnectorDetail{}, err
	}
	var provider *domain.ProviderDeclaration
	if binding := connector.ProviderAccessBinding; withProvider && binding != nil {
		declaration, err := a.declaredProvider(binding.Provider)
		if err != nil {
			return ConnectorDetail{}, err
		}
		provider = &declaration
	}
	if err := change(connector, provider); err != nil {
		return ConnectorDetail{}, err
	}
	if err := a.connectors.Save(ctx, connector); err != nil {
		return ConnectorDetail{}, err
	}
	return a.GetConnector(ctx, command.OwnerID, connector.ID)
}

// ownedConnector loads a Connector the caller may see at the version read;
// whether the caller owns it is the Connector's own decision.
func (a *Application) ownedConnector(ctx context.Context, command ConnectorCommand) (*domain.Connector, error) {
	if strings.TrimSpace(command.OwnerID) == "" {
		return nil, ErrUnauthenticated
	}
	connector, err := a.visibleConnector(ctx, command.OwnerID, command.ConnectorID)
	if err != nil {
		return nil, err
	}
	if connector.Version != command.Version {
		return nil, domain.ErrConcurrentModification
	}
	return connector, nil
}

func (a *Application) declaredProvider(name string) (domain.ProviderDeclaration, error) {
	if a.PersonalConnections == nil || a.providers == nil {
		return domain.ProviderDeclaration{}, ErrProviderUnavailable
	}
	declaration, ok := a.providers.Provider(domain.Provider(name))
	if !ok {
		return domain.ProviderDeclaration{}, ErrProviderUnavailable
	}
	return declaration, nil
}
