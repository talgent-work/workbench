package infrastructure

import (
	"context"
	"strings"
	"time"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type AgentWorkRepository struct {
	db  *xorm.Engine
	now func() time.Time
}

var _ domain.AgentWorkRepository = (*AgentWorkRepository)(nil)

func NewAgentWorkRepository(db *xorm.Engine, now func() time.Time) *AgentWorkRepository {
	return &AgentWorkRepository{db: db, now: now}
}

func (r *AgentWorkRepository) Get(ctx context.Context, id string) (*domain.AgentWork, error) {
	var row agentWorkDO
	found, err := r.db.Context(ctx).Where("id = ? AND deleted_at = 0", id).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "agent_session.get").With("session_id", id).Wrap(err)
	}
	if !found {
		return nil, domain.ErrWorkNotFound
	}
	session, err := sessionFromDO(&row)
	if err != nil {
		return nil, err
	}
	session.WorkShares, err = workShares(r.db.Context(ctx), id)
	return session, err
}

// Save inserts a new Work or updates a loaded one. A new Work registers its
// definition's association: the definition's row lock serializes rule changes
// with creation, and the Work commits only against the definition version it
// was created under. A competing registration of the same identity, a changed
// definition or a Work changed since it was loaded answers
// domain.ErrConcurrentModification.
func (r *AgentWorkRepository) Save(ctx context.Context, session *domain.AgentWork) error {
	if session == nil || session.Version == 0 && !registrable(session) {
		return domain.ErrInvalidWork
	}
	return r.save(ctx, session)
}

// registrable reports whether a new Work may be registered: created under a
// stored definition version, started by someone, and in its admitted first
// start.
func registrable(session *domain.AgentWork) bool {
	starter := strings.TrimSpace(session.StartedBy)
	return session.DefinitionVersion >= 1 && starter != "" && len(starter) <= 255 && session.State == domain.WorkStarting && session.HoldsProcess()
}

func (r *AgentWorkRepository) save(ctx context.Context, session *domain.AgentWork) error {
	row, err := sessionToDO(session)
	if err != nil {
		return err
	}
	row.UpdatedAt = r.now().UTC().UnixMilli()
	row.Version++
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return oops.Wrap(err)
	}
	defer tx.Rollback()
	if session.Version == 0 {
		var definition agentDefinitionDO
		found, lockErr := tx.Cols("version").Where("id = ? AND deleted_at = 0", session.AgentID).ForUpdate().Get(&definition)
		if lockErr != nil {
			return oops.With("operation", "agent_session.lock_definition").Wrap(lockErr)
		}
		if !found {
			return domain.ErrAgentDefinitionNotFound
		}
		if definition.Version != session.DefinitionVersion {
			return domain.ErrConcurrentModification
		}
		row.CreatedAt = row.UpdatedAt
		_, err = tx.Insert(row)
		if duplicateKey(err) {
			return domain.ErrConcurrentModification
		}
	} else {
		var affected int64
		affected, err = tx.Where("id = ? AND version = ? AND deleted_at = 0", row.ID, session.Version).
			Cols("capabilities", "initial_task_input", "repository_checkouts", "thinking_level", "permission_mode", "work_progress", "state", "context_ref", "harness_process", "holds_process", "stop_request", "close_request", "stop_reason", "disconnected_since", "reach_resume_due", "reach_resume_failures", "end_request", "failure_reason", "visibility_public", "display_title", "version", "updated_at").Update(row)
		if err == nil && affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	if err != nil {
		return oops.With("operation", "agent_session.save").With("session_id", session.ID).Wrap(err)
	}
	if err := retainWorkShares(tx, session.ID, session.WorkShares, row.UpdatedAt); err != nil {
		return err
	}
	if err := retainWorkState(tx, session, row); err != nil {
		return err
	}
	return oops.Wrap(tx.Commit())
}
