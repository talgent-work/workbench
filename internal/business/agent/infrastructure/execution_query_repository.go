package infrastructure

import (
	"context"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

type ExecutionQueryRepository struct{ db *xorm.Engine }

var _ query.ExecutionQueryRepository = (*ExecutionQueryRepository)(nil)

func NewExecutionQueryRepository(db *xorm.Engine) *ExecutionQueryRepository {
	return &ExecutionQueryRepository{db: db}
}

// GetWorkStartup reads the Work's first-start request facts. A creation is
// pending while the Work still holds its initial task input, not yet handed
// off to its input buffer.
func (r *ExecutionQueryRepository) GetWorkStartup(ctx context.Context, sessionID string) (query.WorkStartup, error) {
	var row struct {
		SessionID        string `xorm:"session_id"`
		InitialTaskInput string `xorm:"initial_task_input"`
		RequestID        string `xorm:"request_id"`
		RequestData      string `xorm:"request_data"`
		Version          int    `xorm:"version"`
	}
	found, err := r.db.Context(ctx).SQL(`SELECT s.id AS session_id, s.initial_task_input,
 COALESCE(i.request_id,'') AS request_id, COALESCE(i.request_data,'') AS request_data,
 COALESCE(i.version,0) AS version
 FROM agent_session s LEFT JOIN agent_initial_execution_request i
 ON i.id=s.id AND i.deleted_at=0
 WHERE s.id=? AND s.deleted_at=0`, sessionID).Get(&row)
	if err != nil {
		return query.WorkStartup{}, oops.With("operation", "agent.session_startup").Wrap(err)
	}
	if !found {
		return query.WorkStartup{}, domain.ErrWorkNotFound
	}
	result := query.WorkStartup{WorkID: row.SessionID, InitialRequestID: row.RequestID, CreationPending: row.InitialTaskInput != ""}
	if row.RequestID != "" {
		initial, err := initialExecutionRequestFromDO(initialExecutionRequestDO{ID: row.SessionID, RequestID: row.RequestID, RequestData: row.RequestData, Version: row.Version})
		if err != nil {
			return query.WorkStartup{}, err
		}
		result.Rejected, result.CleanupConfirmed = initial.Rejected, initial.CleanupConfirmed
	}
	return result, nil
}

func (r *ExecutionQueryRepository) ListWorkIDs(ctx context.Context, afterID string, limit int) ([]string, error) {
	// Scan Works whose Harness process has outstanding obligations, permanent
	// closure, a close kept while the Work stopped, a Resume owed on its
	// Computer's reconnection, an initial task input not handed off, and an
	// execution not yet retired, whose retirement request a Work that stopped
	// waiting for its release (a withdrawn or fenced start) passes again.
	// Completed historical conversations do not lengthen the recovery scan.
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT session_id FROM (
 SELECT id AS session_id FROM agent_session
 WHERE state IN (1, 2, 3, 5) AND deleted_at = 0 AND JSON_EXTRACT(harness_process, '$.compute_reclaimed') = false
 UNION
 SELECT id AS session_id FROM agent_session WHERE state = 6 AND deleted_at = 0
 UNION
 SELECT id AS session_id FROM agent_session WHERE state IN (4, 5) AND close_request <> '' AND deleted_at = 0
 UNION
 SELECT id AS session_id FROM agent_session WHERE state = 4 AND reach_resume_due = 1 AND deleted_at = 0
 UNION
 SELECT id AS session_id FROM agent_session WHERE state IN (1, 2, 3, 4, 5) AND initial_task_input <> '' AND deleted_at = 0
 UNION
 SELECT live_session_id AS session_id FROM execution_allocation WHERE live_session_id IS NOT NULL
 ) AS pending WHERE session_id > ? ORDER BY session_id LIMIT ?`, afterID, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "agent.list_recovery_sessions").Wrap(err)
	}
	return ids, nil
}

var (
	_ command.ComputerWorks    = (*ExecutionQueryRepository)(nil)
	_ command.ReachResumeWorks = (*ExecutionQueryRepository)(nil)
)

// ListReachResumeWorkIDs lists the Stopped Works on a Computer whose Resume
// it refused as unreachable.
func (r *ExecutionQueryRepository) ListReachResumeWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error) {
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM agent_session
 WHERE computer_id = ? AND state = 4 AND deleted_at = 0 AND id > ? AND reach_resume_due = 1
 ORDER BY id LIMIT ?`, computerID, afterID, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "agent.list_reach_resume_works").Wrap(err)
	}
	return ids, nil
}

// ListRunningWorkIDs lists the Works on a Computer whose run may still be in
// its execution environment: Starting, Working, Stopping or Closing with
// compute not released. A Starting Work still waiting for its Desk is
// listed too; AgentWork itself ignores it.
func (r *ExecutionQueryRepository) ListRunningWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error) {
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM agent_session
 WHERE computer_id = ? AND state IN (1, 2, 3, 6) AND deleted_at = 0 AND id > ?
 AND JSON_EXTRACT(harness_process, '$.compute_reclaimed') = false
 ORDER BY id LIMIT ?`, computerID, afterID, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "agent.list_computer_running_works").Wrap(err)
	}
	return ids, nil
}

// ListHeldWorkIDs lists the Works on a Computer whose run's compute is not
// released: Starting, a start waiting for its Desk included, Working,
// Stopping, Failed or Closing.
func (r *ExecutionQueryRepository) ListHeldWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error) {
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM agent_session
 WHERE computer_id = ? AND state IN (1, 2, 3, 5, 6) AND deleted_at = 0 AND id > ?
 AND JSON_EXTRACT(harness_process, '$.compute_reclaimed') = false
 ORDER BY id LIMIT ?`, computerID, afterID, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "agent.list_computer_held_works").Wrap(err)
	}
	return ids, nil
}

// ListCurrentPluginResults reads the Plugin outcomes that the Work's most
// recently begun execution recorded; an earlier run's outcomes are not
// current.
func (r *ExecutionQueryRepository) ListCurrentPluginResults(ctx context.Context, workID string) ([]query.PluginResult, error) {
	var rows []struct {
		Slug                 string `xorm:"slug"`
		InstallationReported bool   `xorm:"installation_reported"`
		Installed            bool   `xorm:"installed"`
		InstallationReason   string `xorm:"installation_reason"`
		LoadingReported      bool   `xorm:"loading_reported"`
		Loaded               bool   `xorm:"loaded"`
		LoadingReason        string `xorm:"loading_reason"`
		UpdatedAt            int64  `xorm:"updated_at"`
	}
	err := r.db.Context(ctx).SQL(`SELECT p.slug, p.installation_reported, p.installed, p.installation_reason,
 p.loading_reported, p.loaded, p.loading_reason, p.updated_at
 FROM execution_plugin_result p
 WHERE p.deleted_at = 0 AND p.execution_id = (
 SELECT e.id FROM execution_allocation e WHERE e.session_id = ? ORDER BY e.created_at DESC, e.id DESC LIMIT 1)
 ORDER BY p.slug`, workID).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "agent.current_plugin_results", "agent_work_id", workID).Wrap(err)
	}
	results := make([]query.PluginResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, query.PluginResult{Slug: row.Slug, InstallationReported: row.InstallationReported, Installed: row.Installed, InstallationReason: row.InstallationReason,
			LoadingReported: row.LoadingReported, Loaded: row.Loaded, LoadingReason: row.LoadingReason, UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC()})
	}
	return results, nil
}
