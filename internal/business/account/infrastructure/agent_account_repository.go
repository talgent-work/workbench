package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

var agentAccountColumns = []string{"work_id", "project_id", "agent_id", "purpose", "work_subject_kind", "work_subject_id", "represented_user_id", "version", "created_at"}

// AgentAccountRepository persists Agent Accounts. An Agent Account's facts
// never change, so it is only ever inserted.
type AgentAccountRepository struct {
	engine *xorm.Engine
}

var _ domain.AgentAccountRepository = (*AgentAccountRepository)(nil)

func NewAgentAccountRepository(engine *xorm.Engine) *AgentAccountRepository {
	return &AgentAccountRepository{engine: engine}
}

func (r *AgentAccountRepository) Get(ctx context.Context, work domain.WorkID) (*domain.AgentAccount, error) {
	row := new(agentAccountDO)
	found, err := r.engine.Context(ctx).Cols(agentAccountColumns...).Where("work_id = ? AND deleted_at = 0", string(work)).Get(row)
	if err != nil {
		return nil, oops.With("operation", "agent_account.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrAgentAccountNotFound
	}
	account, err := agentAccountFromDO(row)
	if err != nil {
		return nil, oops.With("operation", "agent_account.rehydrate").With("work_id", row.WorkID).Wrap(err)
	}
	return account, nil
}

func (r *AgentAccountRepository) Save(ctx context.Context, account *domain.AgentAccount) error {
	if account.Version != 0 {
		// Nothing of a saved Agent Account changes.
		return nil
	}
	row := agentAccountToDO(account)
	row.Version = 1
	row.UpdatedAt = row.CreatedAt
	if _, err := r.engine.Context(ctx).Insert(row); err != nil {
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == mysqlDuplicateEntry {
			return domain.ErrAgentAccountExists
		}
		return oops.With("operation", "agent_account.insert").With("work_id", row.WorkID).Wrap(err)
	}
	account.Version = row.Version
	return nil
}

func agentAccountToDO(account *domain.AgentAccount) *agentAccountDO {
	facts := account.Facts
	return &agentAccountDO{
		WorkID:            string(account.WorkID),
		ProjectID:         facts.ProjectID,
		AgentID:           facts.AgentID,
		Purpose:           facts.Purpose,
		WorkSubjectKind:   facts.Subject.Kind,
		WorkSubjectID:     facts.Subject.ID,
		RepresentedUserID: string(facts.RepresentedPerson),
		CreatedAt:         account.CreatedAt.UnixMilli(),
	}
}

func agentAccountFromDO(row *agentAccountDO) (*domain.AgentAccount, error) {
	work, err := domain.ParseWorkID(row.WorkID)
	if err != nil {
		return nil, err
	}
	facts, err := domain.NewWorkFacts(row.ProjectID, row.AgentID, row.Purpose, row.WorkSubjectKind, row.WorkSubjectID, row.RepresentedUserID)
	if err != nil {
		return nil, err
	}
	return &domain.AgentAccount{WorkID: work, Facts: facts, CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), Version: row.Version}, nil
}
