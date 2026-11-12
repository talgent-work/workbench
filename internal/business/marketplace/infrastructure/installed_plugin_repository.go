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

type InstalledPluginRepository struct {
	engine *xorm.Engine
}

var _ domain.InstalledPluginRepository = (*InstalledPluginRepository)(nil)
var _ query.InstalledPluginQueryRepository = (*InstalledPluginRepository)(nil)

func NewInstalledPluginRepository(engine *xorm.Engine) *InstalledPluginRepository {
	return &InstalledPluginRepository{engine: engine}
}

func (r *InstalledPluginRepository) Get(ctx context.Context, userID, slug string) (*domain.InstalledPlugin, error) {
	var row installedPluginDO
	found, err := r.engine.Context(ctx).Cols(installationColumns...).Where("owner_id = ? AND slug = ? AND deleted_at = 0", userID, slug).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "marketplace.installation.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrInstallationNotFound
	}
	manual, err := r.configuration(row)
	if err != nil {
		return nil, err
	}
	return installationFromDO(row, manual)
}

func (r *InstalledPluginRepository) Save(ctx context.Context, plugin *domain.InstalledPlugin) error {
	if err := plugin.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(plugin.ManualConfiguration)
	if err != nil {
		return errors.New("marketplace: cannot encode personal configuration")
	}
	row := installationToDO(plugin, string(encoded))
	row.UpdatedAt = time.Now().UnixMilli()
	var affected int64
	if plugin.Version == 0 {
		row.Version, row.CreatedAt = 1, row.UpdatedAt
		affected, err = r.engine.Context(ctx).Insert(&row)
	} else {
		affected, err = r.engine.Context(ctx).Cols("state", "configuration", "updated_at").Incr("version").Where("id = ? AND owner_id = ? AND slug = ? AND version = ? AND deleted_at = 0", plugin.ID, plugin.OwnerID, plugin.Slug, plugin.Version).Update(&row)
	}
	if err != nil {
		var mysqlError *mysqldriver.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return domain.ErrConcurrentModification
		}
		return oops.With("operation", "marketplace.installation.save").Wrap(err)
	}
	if affected != 1 {
		return domain.ErrConcurrentModification
	}
	return nil
}

func (r *InstalledPluginRepository) Delete(ctx context.Context, plugin *domain.InstalledPlugin) error {
	affected, err := r.engine.Context(ctx).Where("id = ? AND owner_id = ? AND slug = ? AND version = ? AND deleted_at = 0", plugin.ID, plugin.OwnerID, plugin.Slug, plugin.Version).Delete(&installedPluginDO{})
	if err != nil {
		return oops.With("operation", "marketplace.installation.delete").Wrap(err)
	}
	if affected != 1 {
		return domain.ErrConcurrentModification
	}
	return nil
}

func (r *InstalledPluginRepository) List(ctx context.Context, userID, afterSlug string, limit int) (query.InstalledPluginPage, error) {
	rows := make([]installedPluginDO, 0, limit+1)
	err := r.engine.Context(ctx).Cols(installationColumns...).Where("owner_id = ? AND slug > ? AND deleted_at = 0", userID, afterSlug).Asc("slug").Limit(limit + 1).Find(&rows)
	if err != nil {
		return query.InstalledPluginPage{}, oops.With("operation", "marketplace.installation.list").Wrap(err)
	}
	page := query.InstalledPluginPage{Plugins: make([]query.InstalledPlugin, 0, len(rows))}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].Slug
		rows = rows[:limit]
	}
	for _, row := range rows {
		manual, err := r.configuration(row)
		if err != nil {
			return query.InstalledPluginPage{}, err
		}
		page.Plugins = append(page.Plugins, installationReadModel(row, manual))
	}
	return page, nil
}

func (r *InstalledPluginRepository) configuration(row installedPluginDO) (domain.Configuration, error) {
	return decodeConfiguration([]byte(row.Configuration))
}
