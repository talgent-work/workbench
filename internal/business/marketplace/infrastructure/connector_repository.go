package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"xorm.io/xorm"
)

type ConnectorRepository struct{ engine *xorm.Engine }

var (
	_ domain.ConnectorRepository              = (*ConnectorRepository)(nil)
	_ domain.InstalledConnectorRepository     = (*InstalledConnectorRepository)(nil)
	_ query.ConnectorQueryRepository          = (*ConnectorRepository)(nil)
	_ query.InstalledConnectorQueryRepository = (*InstalledConnectorRepository)(nil)
)

func NewConnectorRepository(engine *xorm.Engine) *ConnectorRepository {
	return &ConnectorRepository{engine: engine}
}

func (r *ConnectorRepository) GetByName(ctx context.Context, name string) (*domain.Connector, error) {
	var row connectorDO
	found, err := r.engine.Context(ctx).Cols(connectorColumns...).Where("name = ? AND deleted_at = 0", name).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "marketplace.connector.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrConnectorNotFound
	}
	return connectorFromDO(row)
}

func (r *ConnectorRepository) List(ctx context.Context) ([]*domain.Connector, error) {
	var rows []connectorDO
	if err := r.engine.Context(ctx).Cols(connectorColumns...).Where("deleted_at = 0").Asc("name").Find(&rows); err != nil {
		return nil, oops.With("operation", "marketplace.connector.list_aggregates").Wrap(err)
	}
	connectors := make([]*domain.Connector, 0, len(rows))
	for _, row := range rows {
		connector, err := connectorFromDO(row)
		if err != nil {
			return nil, err
		}
		connectors = append(connectors, connector)
	}
	return connectors, nil
}

func (r *ConnectorRepository) Save(ctx context.Context, connector *domain.Connector) error {
	if err := connector.Validate(); err != nil {
		return err
	}
	row, err := connectorToDO(connector)
	if err != nil {
		return err
	}
	row.UpdatedAt = time.Now().UnixMilli()
	var affected int64
	if connector.Version == 0 {
		row.Version, row.CreatedAt = 1, row.UpdatedAt
		affected, err = r.engine.Context(ctx).Insert(&row)
	} else {
		affected, err = r.engine.Context(ctx).Cols("state", "description", "category", "configuration", "updated_at").Incr("version").
			Where("id = ? AND version = ? AND deleted_at = 0", connector.ID, connector.Version).Update(&row)
	}
	return affectedOne(err, affected, "marketplace.connector.save")
}

// ListCatalog reads the whole platform catalog. It is small and platform-owned,
// so one ordered read replaces pagination.
func (r *ConnectorRepository) ListCatalog(ctx context.Context) ([]query.CatalogConnector, error) {
	var rows []connectorDO
	if err := r.engine.Context(ctx).Cols(connectorColumns...).Where("deleted_at = 0").Asc("name").Find(&rows); err != nil {
		return nil, oops.With("operation", "marketplace.connector.list").Wrap(err)
	}
	result := make([]query.CatalogConnector, 0, len(rows))
	for _, row := range rows {
		var configuration connectorConfigurationRecord
		if err := json.Unmarshal([]byte(row.Configuration), &configuration); err != nil {
			return nil, errors.New("marketplace: corrupt Connector configuration")
		}
		item := query.CatalogConnector{ID: row.ID, Name: row.Name, Description: row.Description, Category: row.Category, Required: row.Required,
			Offered: row.State == uint8(domain.ConnectorOffered),
			Configuration: query.ConnectorConfiguration{Type: configuration.Type, URL: configuration.URL, Command: configuration.Command,
				Arguments: configuration.Arguments, Environment: configuration.Environment, Headers: configuration.Headers}}
		for name, value := range connectorAuthentication {
			if value == row.Authentication {
				item.Authentication = name
			}
		}
		if binding := configuration.Provider; binding != nil {
			item.Provider = &query.ConnectorProvider{Provider: binding.Provider, Target: binding.Target, Authentication: binding.Authentication}
		}
		result = append(result, item)
	}
	return result, nil
}

func (r *InstalledConnectorRepository) ListInstalled(ctx context.Context, ownerID string) ([]query.InstalledConnector, error) {
	var rows []installedConnectorDO
	if err := r.engine.Context(ctx).Cols(installedConnectorColumns...).Where("owner_id = ? AND deleted_at = 0", ownerID).Asc("connector_id").Find(&rows); err != nil {
		return nil, oops.With("operation", "marketplace.installed_connector.list").Wrap(err)
	}
	result := make([]query.InstalledConnector, 0, len(rows))
	for _, row := range rows {
		result = append(result, query.InstalledConnector{ConnectorID: row.ConnectorID, Enabled: row.State == uint8(domain.Enabled), Version: row.Version})
	}
	return result, nil
}

type InstalledConnectorRepository struct{ engine *xorm.Engine }

func NewInstalledConnectorRepository(engine *xorm.Engine) *InstalledConnectorRepository {
	return &InstalledConnectorRepository{engine: engine}
}

func (r *InstalledConnectorRepository) Get(ctx context.Context, userID, connectorID string) (*domain.InstalledConnector, error) {
	var row installedConnectorDO
	found, err := r.engine.Context(ctx).Cols(installedConnectorColumns...).Where("owner_id = ? AND connector_id = ? AND deleted_at = 0", userID, connectorID).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "marketplace.installed_connector.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrConnectorInstallationNotFound
	}
	return installedConnectorFromDO(row)
}

func (r *InstalledConnectorRepository) Save(ctx context.Context, installation *domain.InstalledConnector) error {
	if err := installation.Validate(); err != nil {
		return err
	}
	row := installedConnectorToDO(installation)
	row.UpdatedAt = time.Now().UnixMilli()
	var affected int64
	var err error
	if installation.Version == 0 {
		row.Version, row.CreatedAt = 1, row.UpdatedAt
		affected, err = r.engine.Context(ctx).Insert(&row)
	} else {
		affected, err = r.engine.Context(ctx).Cols("state", "updated_at").Incr("version").
			Where("id = ? AND owner_id = ? AND version = ? AND deleted_at = 0", installation.ID, installation.OwnerID, installation.Version).Update(&row)
	}
	return affectedOne(err, affected, "marketplace.installed_connector.save")
}

func (r *InstalledConnectorRepository) Delete(ctx context.Context, installation *domain.InstalledConnector) error {
	affected, err := r.engine.Context(ctx).Where("id = ? AND owner_id = ? AND version = ? AND deleted_at = 0", installation.ID, installation.OwnerID, installation.Version).Delete(&installedConnectorDO{})
	return affectedOne(err, affected, "marketplace.installed_connector.delete")
}

// affectedOne maps duplicate keys and lost optimistic races to one stable error.
func affectedOne(err error, affected int64, operation string) error {
	if err != nil {
		var mysqlError *mysqldriver.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return domain.ErrConcurrentModification
		}
		return oops.With("operation", operation).Wrap(err)
	}
	if affected != 1 {
		return domain.ErrConcurrentModification
	}
	return nil
}
