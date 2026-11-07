package infrastructure

import (
	"context"

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

// One transaction observes both retained request records in one snapshot. In
// particular, an explicit retry can precede a new initial receipt and can still
// have an empty session_id in its creation recovery record; it is correlated by
// the Work's Subject instead.
func (r *ExecutionQueryRepository) GetWorkStartup(ctx context.Context, sessionID string) (query.WorkStartup, error) {
	var row struct {
		SessionID   string  `xorm:"session_id"`
		AgentID     string  `xorm:"agent_id"`
		SubjectKind *string `xorm:"subject_kind"`
		SubjectID   *string `xorm:"subject_id"`
		RequestID   string  `xorm:"request_id"`
		RequestData string  `xorm:"request_data"`
		Version     int     `xorm:"version"`
	}
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return query.WorkStartup{}, oops.Wrap(err)
	}
	defer tx.Rollback()
	found, err := tx.SQL(`SELECT s.id AS session_id, s.agent_id, s.subject_kind, s.subject_id,
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
	pending, err := creationPending(tx, row.AgentID, subjectFromColumns(row.SubjectKind, row.SubjectID), row.RequestID)
	if err != nil {
		return query.WorkStartup{}, oops.With("operation", "agent.session_startup").Wrap(err)
	}
	result := query.WorkStartup{WorkID: row.SessionID, InitialRequestID: row.RequestID, CreationPending: pending}
	if row.RequestID != "" {
		initial, err := initialExecutionRequestFromDO(initialExecutionRequestDO{ID: row.SessionID, RequestID: row.RequestID, RequestData: row.RequestData, Version: row.Version})
		if err != nil {
			return query.WorkStartup{}, err
		}
		result.Rejected, result.CleanupConfirmed = initial.Rejected, initial.CleanupConfirmed
	}
	return result, oops.Wrap(tx.Commit())
}

func (r *ExecutionQueryRepository) ListWorkIDs(ctx context.Context, afterID string, limit int) ([]string, error) {
	// Scan Works whose Harness process has outstanding obligations, permanent
	// closure, a close kept while the Work stopped, a Resume owed on its
	// Computer's reconnection, and an execution not yet retired, whose
	// retirement request a Work that stopped waiting for its release (a
	// withdrawn or fenced start) passes again. Completed historical
	// conversations do not lengthen the recovery scan.
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

// creationPending tells whether a creation request for the Work is still
// pending: one for the same definition and Subject, or, for a Work without a
// Subject, the request that created it.
func creationPending(tx *xorm.Session, agentID string, subject domain.WorkSubject, requestID string) (bool, error) {
	pending := tx.Table("agent_session_creation_request").Where("pending=1 AND deleted_at=0")
	if !subject.Present() {
		if requestID == "" {
			return false, nil
		}
		return pending.And("id=?", requestID).Exist()
	}
	digest, err := subjectDigest(agentID, subject)
	if err != nil {
		return false, err
	}
	return pending.And("subject_digest=?", digest).Exist()
}
