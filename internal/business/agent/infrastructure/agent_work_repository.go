package infrastructure

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"

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

var _ command.WorkRegistrar = (*AgentWorkRepository)(nil)

// RegisterWork persists the definition's association and its controlling
// principal together. The definition lock serializes rule changes with creation.
func (r *AgentWorkRepository) RegisterWork(ctx context.Context, session *domain.AgentWork, registration command.WorkRegistration) error {
	subject, definitionVersion, initial := registration.SubjectID, registration.DefinitionVersion, registration.InitialExecution
	if strings.TrimSpace(subject) == "" || len(subject) > 255 || subject != session.StartedBy {
		return access.ErrAccessDenied
	}
	if session == nil || session.Version != 0 || definitionVersion < 1 || session.State != domain.WorkStarting || !session.HoldsProcess() || uuid.Validate(registration.RequestID) != nil {
		return domain.ErrInvalidWork
	}
	scope, err := registrationScope(subject, session.AgentID, registration.WorkspaceReference, session.Subject, session.ProjectID)
	if err != nil {
		return err
	}
	return r.saveWith(ctx, session, subject, definitionVersion, initial, r.startAdmissionHooks(session, query.AdmittedStart{WorkID: session.ID, Kind: query.StartKindStart}, registration.RequestID, scope))
}

func (r *AgentWorkRepository) Save(ctx context.Context, session *domain.AgentWork) error {
	if session == nil || session.Version == 0 {
		return domain.ErrInvalidWork
	}
	return r.save(ctx, session, "", 0, nil)
}

func (r *AgentWorkRepository) save(ctx context.Context, session *domain.AgentWork, subject string, definitionVersion int, initial *command.PrepareWorkExecution) error {
	return r.saveWith(ctx, session, subject, definitionVersion, initial, saveHooks{})
}

// saveHooks enlist related writes in the Work's transaction: before runs ahead
// of the Work row write, so locks it takes are ordered before that row lock;
// after persists request records once the Work row was written.
type saveHooks struct {
	before, after func(*xorm.Session, int64) error
}

func (r *AgentWorkRepository) saveWith(ctx context.Context, session *domain.AgentWork, subject string, definitionVersion int, initial *command.PrepareWorkExecution, hooks saveHooks) error {
	if session == nil {
		return domain.ErrInvalidWork
	}
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
	if hooks.before != nil {
		if err := hooks.before(tx, row.UpdatedAt); err != nil {
			return err
		}
	}
	if session.Version == 0 {
		var definition agentDefinitionDO
		found, lockErr := tx.Cols("version").Where("id = ? AND deleted_at = 0", session.AgentID).ForUpdate().Get(&definition)
		if lockErr != nil {
			return oops.With("operation", "agent_session.lock_definition").Wrap(lockErr)
		}
		if !found {
			return domain.ErrAgentDefinitionNotFound
		}
		if definition.Version != definitionVersion {
			return domain.ErrConcurrentModification
		}
		if initial != nil && initial.CreationRequestID != "" {
			if err := r.lockWorkCreationRegistration(tx, session, subject, initial); err != nil {
				return err
			}
		}
		row.CreatedAt = row.UpdatedAt
		_, err = tx.Insert(row)
		if duplicateKey(err) {
			return domain.ErrConcurrentModification
		}
	} else {
		var affected int64
		affected, err = tx.Where("id = ? AND version = ? AND deleted_at = 0", row.ID, session.Version).
			Cols("capabilities", "task_execution_begun", "thinking_level", "permission_mode", "work_progress", "state", "context_ref", "harness_process", "holds_process", "stop_request", "close_request", "stop_reason", "disconnected_since", "reach_resume_due", "end_request", "failure_reason", "visibility_public", "display_title", "version", "updated_at").Update(row)
		if err == nil && affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	if err != nil {
		return oops.With("operation", "agent_session.save").With("session_id", session.ID).Wrap(err)
	}
	if initial != nil {
		if session.Version != 0 || initial.WorkID != session.ID || initial.SubjectID != subject {
			return domain.ErrInvalidWork
		}
		receipt, err := initialExecutionRequestToDO(command.InitialExecutionRequest{PrepareWorkExecution: *initial, Submitted: true})
		if err != nil {
			return err
		}
		receipt.Version = 1
		receipt.CreatedAt, receipt.UpdatedAt = row.UpdatedAt, row.UpdatedAt
		if _, err := tx.Insert(&receipt); err != nil {
			return oops.With("operation", "agent_session.retain_initial_input").Wrap(err)
		}
	}
	if hooks.after != nil {
		if err := hooks.after(tx, row.UpdatedAt); err != nil {
			return err
		}
	}
	if err := retainWorkShares(tx, session.ID, session.WorkShares, row.UpdatedAt); err != nil {
		return err
	}
	if err := retainWorkState(tx, session, row); err != nil {
		return err
	}
	return oops.Wrap(tx.Commit())
}
