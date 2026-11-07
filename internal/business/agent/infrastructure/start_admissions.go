package infrastructure

import (
	"context"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (r *AgentWorkRepository) FindStartRequest(ctx context.Context, requestID string) (query.StartRequest, bool, error) {
	var row startRequestDO
	found, err := r.db.Context(ctx).Where("id = ? AND deleted_at = 0", requestID).Get(&row)
	if err != nil {
		return query.StartRequest{}, false, oops.With("operation", "agent.find_start_request").Wrap(err)
	}
	if !found {
		return query.StartRequest{}, false, nil
	}
	return startRequestFromDO(row), true, nil
}

func startRequestFromDO(row startRequestDO) query.StartRequest {
	return query.StartRequest{ID: row.ID, Kind: query.StartKind(row.Kind), WorkID: row.SessionID, Fingerprint: row.Fingerprint, Canceled: row.Canceled}
}

// CommitStart persists the admitted Work together with its start request.
// No definition limits how many of its Works run, so admissions of one
// definition are not serialized.
func (r *AgentWorkRepository) CommitStart(ctx context.Context, work *domain.AgentWork, request query.StartRequest) error {
	if work == nil || work.Version == 0 || request.WorkID != work.ID || request.Canceled || request.Kind == query.StartKindStart {
		return domain.ErrInvalidWork
	}
	return r.saveWith(ctx, work, 0, r.startAdmissionHooks(work, request, ""))
}

// startAdmissionHooks records the request with the Work's commit. The first
// writer of a request identity decides it: a request recorded as cancelled,
// for other content or for another Work or operation fails the commit.
func (r *AgentWorkRepository) startAdmissionHooks(work *domain.AgentWork, request query.StartRequest, creationScope string) saveHooks {
	return saveHooks{after: func(tx *xorm.Session, now int64) error {
		record, err := r.lockStartRequest(tx, request, "", now)
		if err != nil {
			return err
		}
		// A creation cancelled under another registration scope is not this one.
		if record.Canceled && record.CreationScope != "" && record.CreationScope != creationScope {
			return command.ErrStartRequestConflict
		}
		if err := command.AnswerRetained(startRequestFromDO(record), request); err != nil {
			return err
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
	record, err := r.lockStartRequest(tx, query.StartRequest{ID: requestID, WorkID: workID, Canceled: true}, "", r.now().UTC().UnixMilli())
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

// RecordCreation records the outcome of a creation that registered no Work of
// its own, unless its identity is already recorded. Such a creation has no
// Work row to lock; the record's own unique key serializes it with
// RegisterWork, which rolls back its Work if this record won.
func (r *AgentWorkRepository) RecordCreation(ctx context.Context, request query.StartRequest) (query.StartRequest, error) {
	if request.Kind != query.StartKindStart || request.Canceled == (request.WorkID != "") {
		return query.StartRequest{}, domain.ErrInvalidWork
	}
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return query.StartRequest{}, oops.Wrap(err)
	}
	defer tx.Rollback()
	record, err := r.lockStartRequest(tx, request, "", r.now().UTC().UnixMilli())
	if err != nil {
		return query.StartRequest{}, err
	}
	return startRequestFromDO(record), oops.Wrap(tx.Commit())
}

// lockStartRequest inserts the request record unless it exists and returns
// the retained record under its row lock. The first writer decides whether the
// request was admitted or cancelled, and with which content.
func (r *AgentWorkRepository) lockStartRequest(tx *xorm.Session, request query.StartRequest, creationScope string, now int64) (startRequestDO, error) {
	var record startRequestDO
	_, err := tx.Exec(`INSERT INTO agent_start_request
 (id, session_id, kind, fingerprint, canceled, creation_scope, version, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, 0)
 ON DUPLICATE KEY UPDATE id = id`, request.ID, request.WorkID, string(request.Kind), request.Fingerprint, request.Canceled, creationScope, now, now)
	if err != nil {
		return record, oops.With("operation", "agent.retain_start_request").Wrap(err)
	}
	found, err := tx.Where("id = ? AND deleted_at = 0", request.ID).ForUpdate().Get(&record)
	if err != nil {
		return record, oops.Wrap(err)
	}
	if !found {
		return record, domain.ErrConcurrentModification
	}
	return record, nil
}
