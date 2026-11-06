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

type ContinuationRequests struct {
	ProgressCommitted func(context.Context, string)
	db                *xorm.Engine
	now               func() time.Time
}

var _ command.ContinuationRequests = (*ContinuationRequests)(nil)

func NewContinuationRequests(db *xorm.Engine, now func() time.Time) *ContinuationRequests {
	return &ContinuationRequests{db: db, now: now}
}

// Bumping the Session's optimistic token makes a competing lifecycle write
// re-read the retained continuation. No lifecycle field changes here.
func (r *ContinuationRequests) RetainContinuation(ctx context.Context, input command.ContinuationRequest, version int) (command.ContinuationRequest, error) {
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return command.ContinuationRequest{}, oops.Wrap(err)
	}
	defer tx.Rollback()
	var session agentWorkDO
	found, err := tx.Cols("version").Where("id=? AND deleted_at=0", input.WorkID).ForUpdate().Get(&session)
	if err != nil {
		return command.ContinuationRequest{}, oops.Wrap(err)
	}
	if !found {
		return command.ContinuationRequest{}, domain.ErrWorkNotFound
	}
	var row continuationRequestDO
	exists, err := tx.Where("id=? AND deleted_at=0", input.WorkID).ForUpdate().Get(&row)
	if err != nil {
		return command.ContinuationRequest{}, oops.Wrap(err)
	}
	if exists && (row.RequestID == input.RequestID || row.Pending) {
		if row.SubjectID != input.SubjectID || row.Operation != input.Operation {
			return command.ContinuationRequest{}, command.ErrContinuationConflict
		}
		return continuationFromDO(row), oops.Wrap(tx.Commit())
	}
	if session.Version != version {
		return command.ContinuationRequest{}, domain.ErrConcurrentModification
	}
	result, err := tx.Exec("UPDATE agent_session SET version=version+1 WHERE id=? AND version=? AND deleted_at=0", input.WorkID, version)
	if err != nil {
		return command.ContinuationRequest{}, oops.Wrap(err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return command.ContinuationRequest{}, oops.Wrap(err)
	} else if n != 1 {
		return command.ContinuationRequest{}, domain.ErrConcurrentModification
	}
	now := r.now().UTC().UnixMilli()
	if exists {
		result, err = tx.Exec(`UPDATE agent_session_continuation_request
 SET request_id=?,subject_id=?,operation=?,rejection='',pending=1,version=version+1,updated_at=?
 WHERE id=? AND version=? AND deleted_at=0`, input.RequestID, input.SubjectID, input.Operation, now, input.WorkID, row.Version)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				err = domain.ErrConcurrentModification
			}
		}
	} else {
		_, err = tx.Exec(`INSERT INTO agent_session_continuation_request
 (id,request_id,subject_id,operation,rejection,pending,version,created_at,updated_at,deleted_at)
 VALUES (?,?,?,?,'',1,1,?,?,0)`, input.WorkID, input.RequestID, input.SubjectID, input.Operation, now, now)
	}
	if duplicateKey(err) {
		return command.ContinuationRequest{}, command.ErrContinuationConflict
	}
	if err != nil {
		return command.ContinuationRequest{}, oops.Wrap(err)
	}
	if err := tx.Commit(); err != nil {
		return input, oops.Wrap(err)
	}
	if r.ProgressCommitted != nil {
		r.ProgressCommitted(ctx, input.WorkID)
	}
	return input, nil
}

func (r *ContinuationRequests) GetContinuation(ctx context.Context, id string) (command.ContinuationRequest, bool, error) {
	var row continuationRequestDO
	found, err := r.db.Context(ctx).Where("id=? AND deleted_at=0", id).Get(&row)
	return continuationFromDO(row), found, oops.Wrap(err)
}

func (r *ContinuationRequests) RecordContinuationRejection(ctx context.Context, sessionID, requestID, reason string) error {
	result, err := r.db.Context(ctx).Exec(`UPDATE agent_session_continuation_request
 SET rejection=IF(rejection='',?,rejection),version=version+1,updated_at=?
 WHERE id=? AND request_id=? AND (pending=1 OR rejection<>'') AND deleted_at=0`, reason, r.now().UTC().UnixMilli(), sessionID, requestID)
	if err != nil {
		return oops.Wrap(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if count != 1 {
		return command.ErrContinuationConflict
	}
	if r.ProgressCommitted != nil {
		r.ProgressCommitted(ctx, sessionID)
	}
	return nil
}

func (r *ContinuationRequests) ResolveContinuation(ctx context.Context, sessionID, requestID string, admitted bool) error {
	result, err := r.db.Context(ctx).Exec(`UPDATE agent_session_continuation_request
 SET pending=0,rejection=IF(?,'',rejection),version=version+1,updated_at=?
 WHERE id=? AND request_id=? AND deleted_at=0`, admitted, r.now().UTC().UnixMilli(), sessionID, requestID)
	if err != nil {
		return oops.Wrap(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if count != 1 {
		return command.ErrContinuationConflict
	}
	if r.ProgressCommitted != nil {
		r.ProgressCommitted(ctx, sessionID)
	}
	return nil
}

var _ query.ContinuationReader = (*ContinuationRequests)(nil)

func (r *ContinuationRequests) GetWorkContinuation(ctx context.Context, sessionID string) (query.WorkContinuation, error) {
	var row continuationRequestDO
	found, err := r.db.Context(ctx).Cols("request_id", "pending").Where("id=? AND deleted_at=0", sessionID).Get(&row)
	if err != nil {
		return query.WorkContinuation{}, oops.Wrap(err)
	}
	if !found {
		return query.WorkContinuation{}, nil
	}
	return query.WorkContinuation{RequestID: row.RequestID, Pending: row.Pending}, nil
}
func (r *ContinuationRequests) ListPendingContinuations(ctx context.Context, after string, limit int) ([]string, error) {
	var rows []continuationRequestDO
	if err := r.db.Context(ctx).Cols("id").Where("pending=1 AND deleted_at=0 AND id>?", after).Asc("id").Limit(limit).Find(&rows); err != nil {
		return nil, oops.Wrap(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}
