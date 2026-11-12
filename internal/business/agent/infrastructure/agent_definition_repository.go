package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type AgentDefinitionRepository struct {
	db  *xorm.Engine
	now func() time.Time
}

var _ domain.AgentDefinitionRepository = (*AgentDefinitionRepository)(nil)

func NewAgentDefinitionRepository(db *xorm.Engine, now func() time.Time) *AgentDefinitionRepository {
	return &AgentDefinitionRepository{db: db, now: now}
}

func (r *AgentDefinitionRepository) Get(ctx context.Context, id string) (*domain.AgentDefinition, error) {
	var row agentDefinitionDO
	found, err := r.db.Context(ctx).Where("id = ? AND deleted_at = 0", id).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "agent_definition.get").With("agent_id", id).Wrap(err)
	}
	if !found {
		return nil, domain.ErrAgentDefinitionNotFound
	}
	return agentDefinitionFromDO(&row)
}

func (r *AgentDefinitionRepository) Save(ctx context.Context, agent *domain.AgentDefinition) error {
	if agent == nil {
		return domain.ErrInvalidAgentDefinition
	}
	row, err := agentDefinitionToDO(agent)
	if err != nil {
		return err
	}
	row.UpdatedAt = r.now().UTC().UnixMilli()
	row.Version++
	if agent.Version == 0 {
		row.CreatedAt = row.UpdatedAt
		_, err = r.db.Context(ctx).Insert(row)
		if duplicateKey(err) {
			return domain.ErrConcurrentModification
		}
	} else {
		var affected int64
		affected, err = r.db.Context(ctx).Where("id = ? AND version = ? AND deleted_at = 0", row.ID, agent.Version).
			Cols("owners", "agent_name", "icon_glyph", "icon_color", "capabilities", "soul", "application_removed", "version", "updated_at").Update(row)
		if err == nil && affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	if err != nil {
		return oops.With("operation", "agent_definition.save").With("agent_id", agent.ID).Wrap(err)
	}
	return nil
}

func duplicateKey(err error) bool {
	var target *mysql.MySQLError
	return errors.As(err, &target) && target.Number == 1062
}
