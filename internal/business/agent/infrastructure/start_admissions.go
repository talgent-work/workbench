package infrastructure

import (
	"context"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (r *AgentWorkRepository) FindAdmittedStart(ctx context.Context, requestID string) (query.AdmittedStart, bool, error) {
	var row startRequestDO
	found, err := r.db.Context(ctx).Where("id = ? AND canceled = 0 AND deleted_at = 0", requestID).Get(&row)
	if err != nil {
		return query.AdmittedStart{}, false, oops.With("operation", "agent.find_admitted_start").Wrap(err)
	}
	if !found {
		return query.AdmittedStart{}, false, nil
	}
	return query.AdmittedStart{WorkID: row.SessionID, Kind: query.StartKind(row.Kind)}, true, nil
}

// CommitStart persists the admitted Work together with its start request.
// No definition limits how many of its Works run, so admissions of one
// definition are not serialized.
func (r *AgentWorkRepository) CommitStart(ctx context.Context, work *domain.AgentWork, start query.AdmittedStart, requestID string) error {
	if work == nil || work.Version == 0 || start.WorkID != work.ID {
		return domain.ErrInvalidWork
	}
	return r.saveWith(ctx, work, "", 0, nil, r.startAdmissionHooks(work, start, requestID, ""))
}

func (r *AgentWorkRepository) startAdmissionHooks(work *domain.AgentWork, start query.AdmittedStart, requestID string, creationScope string) saveHooks {
	return saveHooks{after: func(tx *xorm.Session, now int64) error {
		record, err := r.lockStartRequest(tx, work.ID, requestID, string(start.Kind), false, now)
		if err != nil {
			return err
		}
		if record.Canceled && record.SessionID == "" && creationScope != "" && record.CreationScope == creationScope {
			return command.ErrStartRequestCanceled
		}
		if record.SessionID != work.ID {
			return command.ErrStartRequestConflict
		}
		if record.Canceled {
			return command.ErrStartRequestCanceled
		}
		if record.Kind != string(start.Kind) {
			return command.ErrStartRequestConflict
		}
		if start.Kind == query.StartKindRestart && !work.TaskExecutionBegun {
			if err := retryInitialPreparation(tx, work.ID, now); err != nil {
				return err
			}
		}
		// Input not yet written stays in the Work's input buffer for the new
		// process.
		return nil
	}}
}

// CancelStart cancels a start request that has not been admitted, under the
// Work row lock that admission also takes. It returns false when the request
// was admitted for this Work.
func (r *AgentWorkRepository) CancelStart(ctx context.Context, workID, requestID string) (bool, error) {
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return false, oops.Wrap(err)
	}
	defer tx.Rollback()
	var session agentWorkDO
	found, err := tx.Cols("id").Where("id = ? AND deleted_at = 0", workID).ForUpdate().Get(&session)
	if err != nil {
		return false, oops.Wrap(err)
	}
	if !found {
		return false, domain.ErrWorkNotFound
	}
	record, err := r.lockStartRequest(tx, workID, requestID, "", true, r.now().UTC().UnixMilli())
	if err != nil {
		return false, err
	}
	if record.SessionID != workID {
		return false, command.ErrStartRequestConflict
	}
	if !record.Canceled {
		return false, nil
	}
	return true, oops.Wrap(tx.Commit())
}

// lockStartRequest inserts the request record unless it exists and returns
// the retained record under its row lock. The first writer decides whether the
// request was admitted or canceled.
func (r *AgentWorkRepository) lockStartRequest(tx *xorm.Session, workID, requestID, kind string, canceled bool, now int64) (startRequestDO, error) {
	var record startRequestDO
	_, err := tx.Exec(`INSERT INTO agent_start_request
 (id, session_id, kind, canceled, version, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, 1, ?, ?, 0)
 ON DUPLICATE KEY UPDATE id = id`, requestID, workID, kind, canceled, now, now)
	if err != nil {
		return record, oops.With("operation", "agent.retain_start_request").Wrap(err)
	}
	found, err := tx.Where("id = ? AND deleted_at = 0", requestID).ForUpdate().Get(&record)
	if err != nil {
		return record, oops.Wrap(err)
	}
	if !found {
		return record, domain.ErrConcurrentModification
	}
	return record, nil
}

// The initial obligation and explicit Restart commit under the same Work lock.
// A concurrent recovery Save then fails its request revision check instead of
// restoring the old rejection or releasing retained input after Restart.
func retryInitialPreparation(tx *xorm.Session, workID string, now int64) error {
	var row initialExecutionRequestDO
	found, err := tx.Where("id = ? AND deleted_at = 0", workID).ForUpdate().Get(&row)
	if err != nil {
		return oops.Wrap(err)
	}
	if !found {
		return nil
	}
	request, err := initialExecutionRequestFromDO(row)
	if err != nil {
		return err
	}
	changed, err := request.RetryPreparation()
	if err != nil || !changed {
		return err
	}
	updated, err := initialExecutionRequestToDO(request)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE agent_initial_execution_request SET request_data = ?, pending = ?, version = version + 1, updated_at = ? WHERE id = ? AND deleted_at = 0", updated.RequestData, updated.Pending, now, workID)
	return oops.Wrap(err)
}
