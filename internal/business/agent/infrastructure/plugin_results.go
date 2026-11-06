package infrastructure

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"xorm.io/xorm"
)

type PluginResults struct {
	db  *xorm.Engine
	now func() time.Time
}

var _ application.PluginResults = (*PluginResults)(nil)

func NewPluginResults(db *xorm.Engine, now func() time.Time) *PluginResults {
	return &PluginResults{db: db, now: now}
}

func (r *PluginResults) Read(ctx context.Context, workID string) ([]query.PluginResult, error) {
	var rows []struct {
		Slug                 string `xorm:"slug"`
		InstallationObserved bool   `xorm:"installation_observed"`
		Installed            bool   `xorm:"installed"`
		Reason               string `xorm:"reason"`
		LoadObserved         bool   `xorm:"load_observed"`
		Loaded               bool   `xorm:"loaded"`
		LoadReason           string `xorm:"load_reason"`
		UpdatedAt            int64  `xorm:"updated_at"`
	}
	err := r.db.Context(ctx).Table("agent_plugin_result").Cols("slug", "installation_observed", "installed", "reason", "load_observed", "loaded", "load_reason", "updated_at").Where("work_id=? AND deleted_at=0", workID).Asc("slug").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "read_plugin_results").Wrap(err)
	}
	result := make([]query.PluginResult, 0, len(rows))
	for _, row := range rows {
		result = append(result, query.PluginResult{Slug: row.Slug, InstallationObserved: row.InstallationObserved, Installed: row.Installed, InstallationReason: row.Reason, LoadObserved: row.LoadObserved, Loaded: row.Loaded, LoadReason: row.LoadReason, UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC()})
	}
	return result, nil
}

func (r *PluginResults) RecordLoads(ctx context.Context, workID string, results []application.PluginLoadResult) error {
	if len(results) == 0 {
		return nil
	}
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return oops.With("operation", "record_plugin_loads").Wrap(err)
	}
	defer tx.Rollback()
	now := r.now().UnixMilli()
	for _, result := range results {
		_, err := tx.Exec(`INSERT INTO agent_plugin_result
 (id,work_id,slug,load_observed,loaded,load_reason,version,created_at,updated_at,deleted_at)
 VALUES (?,?,?,1,?,?,1,?,?,0)
 ON DUPLICATE KEY UPDATE load_observed=1,loaded=VALUES(loaded),load_reason=VALUES(load_reason),version=version+1,updated_at=VALUES(updated_at)`,
			uuid.Must(uuid.NewV7()).String(), workID, result.Slug, result.Loaded, result.Reason, now, now)
		if err != nil {
			return oops.With("operation", "record_plugin_loads").Wrap(err)
		}
	}
	return oops.With("operation", "commit_plugin_loads").Wrap(tx.Commit())
}
