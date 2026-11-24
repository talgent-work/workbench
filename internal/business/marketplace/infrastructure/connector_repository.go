package infrastructure

import (
	"context"
	"errors"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type ConnectorRepository struct{ db *jimuxorm.DB }

var (
	_ domain.ConnectorRepository              = (*ConnectorRepository)(nil)
	_ domain.InstalledConnectorRepository     = (*InstalledConnectorRepository)(nil)
	_ query.ConnectorQueryRepository          = (*ConnectorRepository)(nil)
	_ query.InstalledConnectorQueryRepository = (*InstalledConnectorRepository)(nil)
)

func NewConnectorRepository(db *jimuxorm.DB) *ConnectorRepository {
	return &ConnectorRepository{db: db}
}

func (r *ConnectorRepository) Get(ctx context.Context, id string) (*domain.Connector, error) {
	return r.get(ctx, "id = ? AND deleted_at = 0", id)
}

func (r *ConnectorRepository) GetPlatform(ctx context.Context, name string) (*domain.Connector, error) {
	return r.get(ctx, "owner_kind = ? AND owner_id = '' AND name = ? AND deleted_at = 0", uint8(domain.OwnerPlatform), name)
}

func (r *ConnectorRepository) get(ctx context.Context, condition string, arguments ...any) (*domain.Connector, error) {
	var row connectorDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(connectorAggregateColumns...).Where(condition, arguments...).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "marketplace.connector.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrConnectorNotFound
	}
	return connectorFromDO(row)
}

// Save inserts a new Connector or applies a change with an optimistic version
// check. A duplicate name of one owner is ErrConnectorNameTaken; concurrent
// establishment of one Platform Connector is ErrConcurrentModification.
func (r *ConnectorRepository) Save(ctx context.Context, connector *domain.Connector) error {
	if err := connector.Validate(); err != nil {
		return err
	}
	row, err := connectorToDO(connector)
	if err != nil {
		return err
	}
	row.UpdatedAt = time.Now().UnixMilli()
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	var affected int64
	if connector.Version == 0 {
		row.Version, row.CreatedAt = 1, row.UpdatedAt
		affected, err = session.Insert(&row)
		if duplicateKey(err) && !connector.Owner.Platform() {
			return domain.ErrConnectorNameTaken
		}
	} else {
		affected, err = session.Cols(connectorChangeColumns...).Incr("version").
			Where("id = ? AND version = ? AND deleted_at = 0", connector.ID, connector.Version).Update(&row)
	}
	return affectedOne(err, affected, "marketplace.connector.save")
}

// Delete removes the row, so its owner may add the name again.
func (r *ConnectorRepository) Delete(ctx context.Context, connector *domain.Connector) error {
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	affected, err := session.Where("id = ? AND version = ? AND state = ? AND deleted_at = 0", connector.ID, connector.Version, uint8(domain.ConnectorPrivate)).Delete(&connectorDO{})
	return affectedOne(err, affected, "marketplace.connector.delete")
}

// ListForOwner reads the owner's Connector catalog, every Connector the owner
// made and every Withdrawn one the owner installed, ordered by name.
func (r *ConnectorRepository) ListForOwner(ctx context.Context, owner string) ([]query.CatalogConnector, error) {
	return r.list(ctx, "marketplace.connector.list_for_owner",
		"deleted_at = 0 AND (state = ? OR owner_id = ? OR id IN (SELECT connector_id FROM marketplace_installed_connector WHERE owner_id = ? AND deleted_at = 0))",
		uint8(domain.ConnectorPublished), owner, owner)
}

// ListPublished reads the Connectors every owner sees, ordered by name.
func (r *ConnectorRepository) ListPublished(ctx context.Context) ([]query.CatalogConnector, error) {
	return r.list(ctx, "marketplace.connector.list_published", "deleted_at = 0 AND state = ?", uint8(domain.ConnectorPublished))
}

func (r *ConnectorRepository) list(ctx context.Context, operation, condition string, arguments ...any) ([]query.CatalogConnector, error) {
	var rows []connectorDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Cols(connectorColumns...).Where(condition, arguments...).Asc("name", "id").Find(&rows); err != nil {
		return nil, oops.With("operation", operation).Wrap(err)
	}
	result := make([]query.CatalogConnector, 0, len(rows))
	for _, row := range rows {
		item, err := catalogConnectorFromDO(row)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (r *InstalledConnectorRepository) ListInstalled(ctx context.Context, ownerID string) ([]query.InstalledConnector, error) {
	var rows []installedConnectorDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Cols(installedConnectorColumns...).Where("owner_id = ? AND deleted_at = 0", ownerID).Asc("connector_id").Find(&rows); err != nil {
		return nil, oops.With("operation", "marketplace.installed_connector.list").Wrap(err)
	}
	result := make([]query.InstalledConnector, 0, len(rows))
	for _, row := range rows {
		result = append(result, query.InstalledConnector{ConnectorID: row.ConnectorID, Enabled: row.State == uint8(domain.Enabled), Version: row.Version})
	}
	return result, nil
}

type InstalledConnectorRepository struct{ db *jimuxorm.DB }

func NewInstalledConnectorRepository(db *jimuxorm.DB) *InstalledConnectorRepository {
	return &InstalledConnectorRepository{db: db}
}

func (r *InstalledConnectorRepository) Get(ctx context.Context, userID, connectorID string) (*domain.InstalledConnector, error) {
	var row installedConnectorDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(installedConnectorColumns...).Where("owner_id = ? AND connector_id = ? AND deleted_at = 0", userID, connectorID).Get(&row)
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
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	var affected int64
	if installation.Version == 0 {
		row.Version, row.CreatedAt = 1, row.UpdatedAt
		affected, err = session.Insert(&row)
	} else {
		affected, err = session.Cols("state", "updated_at").Incr("version").
			Where("id = ? AND owner_id = ? AND version = ? AND deleted_at = 0", installation.ID, installation.OwnerID, installation.Version).Update(&row)
	}
	return affectedOne(err, affected, "marketplace.installed_connector.save")
}

func (r *InstalledConnectorRepository) Delete(ctx context.Context, installation *domain.InstalledConnector) error {
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	affected, err := session.Where("id = ? AND owner_id = ? AND version = ? AND deleted_at = 0", installation.ID, installation.OwnerID, installation.Version).Delete(&installedConnectorDO{})
	return affectedOne(err, affected, "marketplace.installed_connector.delete")
}

func duplicateKey(err error) bool {
	var mysqlError *mysqldriver.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

// affectedOne maps duplicate keys and lost optimistic races to one stable error.
func affectedOne(err error, affected int64, operation string) error {
	if err != nil {
		if duplicateKey(err) {
			return domain.ErrConcurrentModification
		}
		return oops.With("operation", operation).Wrap(err)
	}
	if affected != 1 {
		return domain.ErrConcurrentModification
	}
	return nil
}
