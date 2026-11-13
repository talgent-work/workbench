package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

var agentAccountColumns = []string{"work_id", "work_caller_kind", "work_caller_id", "work_key_kind", "work_key_id", "file_home_kind", "file_home_id", "agent_id", "purpose", "represented_user_id", "application_id", "work_labels", "version", "created_at"}

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
		WorkCallerKind:    facts.WorkCaller.Kind,
		WorkCallerID:      facts.WorkCaller.ID,
		WorkKeyKind:       facts.WorkKey.Kind,
		WorkKeyID:         facts.WorkKey.ID,
		FileHomeKind:      facts.FileHome.Kind,
		FileHomeID:        facts.FileHome.ID,
		AgentID:           facts.AgentID,
		Purpose:           facts.Purpose,
		RepresentedUserID: string(facts.RepresentedPerson),
		ApplicationID:     string(facts.OwningApplication),
		WorkLabels:        encodeWorkLabels(facts.WorkLabels),
		CreatedAt:         account.CreatedAt.UnixMilli(),
	}
}

// encodeWorkLabels keeps the labels as a JSON object, an empty one for none.
func encodeWorkLabels(labels domain.WorkLabels) string {
	pairs := labels.Map()
	if pairs == nil {
		pairs = map[string]string{}
	}
	encoded, _ := json.Marshal(pairs) // A map of strings always encodes.
	return string(encoded)
}

func agentAccountFromDO(row *agentAccountDO) (*domain.AgentAccount, error) {
	work, err := domain.ParseWorkID(row.WorkID)
	if err != nil {
		return nil, err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(row.WorkLabels), &labels); err != nil {
		return nil, err
	}
	facts, err := domain.NewWorkFacts(
		domain.WorkCaller{Kind: row.WorkCallerKind, ID: row.WorkCallerID},
		domain.WorkKey{Kind: row.WorkKeyKind, ID: row.WorkKeyID},
		domain.FileHome{Kind: row.FileHomeKind, ID: row.FileHomeID},
		row.AgentID, row.Purpose, row.RepresentedUserID, row.ApplicationID, labels)
	if err != nil {
		return nil, err
	}
	return &domain.AgentAccount{WorkID: work, Facts: facts, CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), Version: row.Version}, nil
}
