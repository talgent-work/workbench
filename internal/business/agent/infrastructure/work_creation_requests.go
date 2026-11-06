package infrastructure

import (
	"context"
	"reflect"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// The caller's Session and initial-input transaction owns this lock. Recording
// the identity here is rolled back if either registration write later fails.
func (r *AgentWorkRepository) lockWorkCreationRegistration(tx *xorm.Session, session *domain.AgentWork, subject string, initial *command.PrepareWorkExecution) error {
	var row workCreationDO
	found, err := tx.Where("id=? AND deleted_at=0", initial.CreationRequestID).ForUpdate().Get(&row)
	if err != nil {
		return oops.Wrap(err)
	}
	if !found {
		return command.ErrWorkCreationNotFound
	}
	retained, err := workCreationFromDO(row)
	if err != nil {
		return err
	}
	if retained.Canceled {
		return command.ErrWorkCreationCanceled
	}
	matched := *initial
	matched.WorkID = ""
	if retained.Creation.AgentID != session.AgentID || retained.Creation.SubjectID != subject || retained.Creation.Subject != session.Subject || retained.Creation.ProjectID != session.ProjectID || !reflect.DeepEqual(matched, *retained.Creation.InitialExecution) {
		return command.ErrWorkCreationConflict
	}
	if retained.WorkID != "" && retained.WorkID != session.ID {
		return domain.ErrConcurrentModification
	}
	_, err = tx.Exec("UPDATE agent_session_creation_request SET session_id=?,version=version+1,updated_at=? WHERE id=?", session.ID, r.now().UTC().UnixMilli(), retained.ID)
	return oops.Wrap(err)
}

type WorkCreationRequests struct {
	db  *xorm.Engine
	now func() time.Time
}

var _ command.WorkCreationRequests = (*WorkCreationRequests)(nil)

func NewWorkCreationRequests(db *xorm.Engine, now func() time.Time) *WorkCreationRequests {
	return &WorkCreationRequests{db: db, now: now}
}

func (r *WorkCreationRequests) RetainWorkCreation(ctx context.Context, request command.WorkCreationRequest) (command.WorkCreationRequest, error) {
	row, err := workCreationToDO(request)
	if err != nil {
		return command.WorkCreationRequest{}, err
	}
	now := r.now().UTC().UnixMilli()
	_, err = r.db.Context(ctx).Exec(`INSERT INTO agent_session_creation_request
 (id,subject_digest,request_data,version,created_at,updated_at,deleted_at) VALUES (?,?,?,1,?,?,0)
 ON DUPLICATE KEY UPDATE id=id`, row.ID, row.SubjectDigest, row.RequestData, now, now)
	if err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	retained, err := r.GetWorkCreation(ctx, request.ID)
	if err != nil {
		return command.WorkCreationRequest{}, err
	}
	// A cancelled identity answers cancelled, whatever the repeat carries.
	if retained.Canceled {
		return retained, nil
	}
	if !reflect.DeepEqual(retained.Creation, request.Creation) {
		return command.WorkCreationRequest{}, command.ErrWorkCreationConflict
	}
	return retained, nil
}

func (r *WorkCreationRequests) GetWorkCreation(ctx context.Context, id string) (command.WorkCreationRequest, error) {
	var row workCreationDO
	found, err := r.db.Context(ctx).Where("id=? AND deleted_at=0", id).Get(&row)
	if err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	if !found {
		return command.WorkCreationRequest{}, command.ErrWorkCreationNotFound
	}
	return workCreationFromDO(row)
}

func (r *WorkCreationRequests) CancelWorkCreation(ctx context.Context, id string) (command.WorkCreationRequest, error) {
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	defer tx.Rollback()
	now := r.now().UTC().UnixMilli()
	// An identity Agent has not received yet is fenced by a cancelled record,
	// so a delayed request carrying it creates nothing.
	if _, err := tx.Exec(`INSERT INTO agent_session_creation_request
 (id,subject_digest,request_data,canceled,pending,version,created_at,updated_at,deleted_at) VALUES (?,'',?,1,0,1,?,?,0)
 ON DUPLICATE KEY UPDATE id=id`, id, cancelledCreationData, now, now); err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	var row workCreationDO
	found, err := tx.Where("id=? AND deleted_at=0", id).ForUpdate().Get(&row)
	if err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	if !found {
		return command.WorkCreationRequest{}, command.ErrWorkCreationNotFound
	}
	retained, err := workCreationFromDO(row)
	if err != nil {
		return command.WorkCreationRequest{}, err
	}
	if row.SessionID == "" && !row.Canceled {
		if _, err := tx.Exec("UPDATE agent_session_creation_request SET canceled=1,pending=0,version=version+1,updated_at=? WHERE id=?", now, id); err != nil {
			return command.WorkCreationRequest{}, oops.Wrap(err)
		}
		retained.Canceled = true
	}
	if err := tx.Commit(); err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	return retained, nil
}

func (r *WorkCreationRequests) ConfirmWorkCreation(ctx context.Context, id, sessionID string) error {
	if sessionID == "" {
		return command.ErrWorkCreationConflict
	}
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return oops.Wrap(err)
	}
	defer tx.Rollback()
	var row workCreationDO
	found, err := tx.Where("id=? AND deleted_at=0", id).ForUpdate().Get(&row)
	if err != nil {
		return oops.Wrap(err)
	}
	if !found {
		return command.ErrWorkCreationNotFound
	}
	retained, err := workCreationFromDO(row)
	if err != nil {
		return err
	}
	if retained.Canceled {
		return command.ErrWorkCreationCanceled
	}
	if retained.WorkID != "" && retained.WorkID != sessionID {
		return command.ErrWorkCreationConflict
	}
	var actual agentWorkDO
	found, err = tx.Where("id=? AND agent_id=? AND started_by=? AND deleted_at=0", sessionID, retained.Creation.AgentID, retained.Creation.SubjectID).Get(&actual)
	if err != nil {
		return oops.Wrap(err)
	}
	if !found {
		return command.ErrWorkCreationConflict
	}
	session, err := sessionFromDO(&actual)
	if err != nil {
		return err
	}
	if session.Subject != retained.Creation.Subject || session.ProjectID != retained.Creation.ProjectID {
		return command.ErrWorkCreationConflict
	}
	if retained.BindingConfirmed {
		return nil
	}
	_, err = tx.Exec(`UPDATE agent_session_creation_request
 SET session_id=?,binding_confirmed=1,version=version+1,updated_at=?
 WHERE id=? AND canceled=0 AND (session_id='' OR session_id=?) AND deleted_at=0`, sessionID, r.now().UTC().UnixMilli(), id, sessionID)
	if err != nil {
		return oops.Wrap(err)
	}
	return oops.Wrap(tx.Commit())
}

func (r *WorkCreationRequests) ListPendingWorkCreations(ctx context.Context, after string, limit int) ([]string, error) {
	var rows []workCreationDO
	if err := r.db.Context(ctx).Cols("id").Where("pending=1 AND id>? AND deleted_at=0", after).Asc("id").Limit(limit).Find(&rows); err != nil {
		return nil, oops.Wrap(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// Completion follows the durable handoff to initial preparation, including a
// rejected explicit retry. Confirmation alone cannot discard that obligation.
func (r *WorkCreationRequests) CompleteWorkCreation(ctx context.Context, id string) error {
	result, err := r.db.Context(ctx).Exec(`UPDATE agent_session_creation_request
 SET pending=0,version=version+1,updated_at=? WHERE id=? AND binding_confirmed=1 AND canceled=0 AND deleted_at=0`, r.now().UTC().UnixMilli(), id)
	if err != nil {
		return oops.Wrap(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if affected != 1 {
		return command.ErrWorkCreationConflict
	}
	return nil
}
