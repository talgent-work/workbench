package execution

import (
	"context"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// pluginResultDO is one selected Plugin's preparation outcome in one
// execution's run, owned by the execution's row.
type pluginResultDO struct {
	ID                   string `xorm:"id pk"`
	ExecutionID          string `xorm:"execution_id"`
	Slug                 string `xorm:"slug"`
	InstallationReported bool   `xorm:"installation_reported"`
	Installed            bool   `xorm:"installed"`
	InstallationReason   string `xorm:"installation_reason"`
	LoadingReported      bool   `xorm:"loading_reported"`
	Loaded               bool   `xorm:"loaded"`
	LoadingReason        string `xorm:"loading_reason"`
	LoadedVersion        string `xorm:"loaded_version"`
	Version              int    `xorm:"'version'"`
	CreatedAt            int64  `xorm:"created_at"`
	UpdatedAt            int64  `xorm:"updated_at"`
	DeletedAt            int64  `xorm:"deleted_at"`
}

func (*pluginResultDO) TableName() string { return "execution_plugin_result" }

// pluginResultColumns are the columns holding an outcome.
var pluginResultColumns = []string{"installation_reported", "installed", "installation_reason", "loading_reported", "loaded", "loading_reason", "loaded_version", "version", "updated_at"}

func pluginResultToDO(executionID string, result domain.PluginResult) pluginResultDO {
	return pluginResultDO{
		ExecutionID: executionID, Slug: result.Slug,
		InstallationReported: result.Installation.Reported, Installed: result.Installation.Succeeded, InstallationReason: result.Installation.Reason,
		LoadingReported: result.Loading.Reported, Loaded: result.Loading.Succeeded, LoadingReason: result.Loading.Reason, LoadedVersion: result.LoadedVersion,
	}
}

func pluginResultFromDO(row pluginResultDO) domain.PluginResult {
	return domain.PluginResult{
		Slug:          row.Slug,
		Installation:  domain.PluginOutcome{Reported: row.InstallationReported, Succeeded: row.Installed, Reason: row.InstallationReason},
		Loading:       domain.PluginOutcome{Reported: row.LoadingReported, Succeeded: row.Loaded, Reason: row.LoadingReason},
		LoadedVersion: row.LoadedVersion,
	}
}

// pluginResults reads the execution's Plugin outcomes.
func pluginResults(ctx context.Context, db *xorm.Engine, executionID string) ([]domain.PluginResult, error) {
	var rows []pluginResultDO
	if err := db.Context(ctx).Where("execution_id = ? AND deleted_at = 0", executionID).Asc("slug").Find(&rows); err != nil {
		return nil, oops.With("operation", "execution.plugin_results", "execution_id", executionID).Wrap(err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	results := make([]domain.PluginResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, pluginResultFromDO(row))
	}
	return results, nil
}

// savePluginResults writes, within the execution's save, the outcomes that
// differ from the stored ones. Outcomes are replaced, never removed.
func savePluginResults(tx *xorm.Session, executionID string, results []domain.PluginResult, now int64) error {
	if len(results) == 0 {
		return nil
	}
	var stored []pluginResultDO
	if err := tx.Where("execution_id = ? AND deleted_at = 0", executionID).Find(&stored); err != nil {
		return oops.With("operation", "execution.read_plugin_results", "execution_id", executionID).Wrap(err)
	}
	bySlug := make(map[string]pluginResultDO, len(stored))
	for _, row := range stored {
		bySlug[row.Slug] = row
	}
	for _, result := range results {
		row := pluginResultToDO(executionID, result)
		current, found := bySlug[result.Slug]
		if !found {
			row.ID, row.Version, row.CreatedAt, row.UpdatedAt = uuid.Must(uuid.NewV7()).String(), 1, now, now
			if _, err := tx.Insert(&row); err != nil {
				return oops.With("operation", "execution.insert_plugin_result", "execution_id", executionID).Wrap(err)
			}
			continue
		}
		if pluginResultFromDO(current) == pluginResultFromDO(row) {
			continue
		}
		row.Version, row.UpdatedAt = current.Version+1, now
		changed, err := tx.Cols(pluginResultColumns...).Where("id = ? AND version = ?", current.ID, current.Version).Update(&row)
		if err != nil {
			return oops.With("operation", "execution.update_plugin_result", "execution_id", executionID).Wrap(err)
		}
		if changed != 1 {
			return domain.ErrConcurrentModification
		}
	}
	return nil
}
