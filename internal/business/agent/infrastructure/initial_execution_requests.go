package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

type InitialExecutionRequests struct {
	ProgressCommitted func(context.Context, string)
	db                *xorm.Engine
	now               func() time.Time
}

var _ command.InitialExecutionRequests = (*InitialExecutionRequests)(nil)
var _ query.WorkPreparationReader = (*InitialExecutionRequests)(nil)

func NewInitialExecutionRequests(db *xorm.Engine, now func() time.Time) *InitialExecutionRequests {
	return &InitialExecutionRequests{db: db, now: now}
}

func (r *InitialExecutionRequests) Get(ctx context.Context, sessionID string) (command.InitialExecutionRequest, error) {
	var row initialExecutionRequestDO
	found, err := r.db.Context(ctx).Cols("id", "request_id", "request_data", "version").Where("id=? AND deleted_at=0", sessionID).Get(&row)
	if err != nil {
		return command.InitialExecutionRequest{}, oops.Wrap(err)
	}
	if !found {
		return command.InitialExecutionRequest{}, command.ErrInitialExecutionNotFound
	}
	return initialExecutionRequestFromDO(row)
}

func (r *InitialExecutionRequests) Save(ctx context.Context, request command.InitialExecutionRequest) error {
	row, err := initialExecutionRequestToDO(request)
	if err != nil {
		return err
	}
	result, err := r.db.Context(ctx).Exec(`UPDATE agent_initial_execution_request
 SET request_data=?,pending=?,submitted=?,version=version+1,updated_at=?
 WHERE id=? AND request_id=? AND version=? AND deleted_at=0`, row.RequestData, row.Pending, row.Submitted, r.now().UTC().UnixMilli(), row.ID, row.RequestID, row.Version)
	if err != nil {
		return oops.Wrap(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if affected != 1 {
		return domain.ErrConcurrentModification
	}
	if r.ProgressCommitted != nil {
		r.ProgressCommitted(ctx, request.WorkID)
	}
	return nil
}

func (r *InitialExecutionRequests) ListPendingInitialExecutions(ctx context.Context, after string, limit int) ([]string, error) {
	var rows []initialExecutionRequestDO
	if err := r.db.Context(ctx).Cols("id").Where("pending=1 AND deleted_at=0 AND id>?", after).Asc("id").Limit(limit).Find(&rows); err != nil {
		return nil, oops.Wrap(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

func (r *InitialExecutionRequests) GetWorkPreparation(ctx context.Context, sessionID string) (query.WorkPreparation, bool, error) {
	request, err := r.Get(ctx, sessionID)
	if errors.Is(err, command.ErrInitialExecutionNotFound) {
		return query.WorkPreparation{}, false, nil
	}
	if err != nil {
		return query.WorkPreparation{}, false, err
	}
	return sessionPreparationFromRequest(request), true, nil
}

func (r *InitialExecutionRequests) ListSubmittedPreparations(ctx context.Context, after string, limit int) ([]query.WorkPreparation, error) {
	var rows []initialExecutionRequestDO
	if err := r.db.Context(ctx).Cols("id", "request_id", "request_data", "version").Where("submitted=1 AND id>? AND deleted_at=0", after).Asc("id").Limit(limit).Find(&rows); err != nil {
		return nil, oops.Wrap(err)
	}
	result := make([]query.WorkPreparation, 0, len(rows))
	for _, row := range rows {
		request, err := initialExecutionRequestFromDO(row)
		if err != nil {
			return nil, err
		}
		result = append(result, sessionPreparationFromRequest(request))
	}
	return result, nil
}
