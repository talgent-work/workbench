package infrastructure

import (
	"context"
	"strings"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

type AgentDefinitionLookup struct{ db *xorm.Engine }

var (
	_ query.AgentDefinitionLookup = (*AgentDefinitionLookup)(nil)
	_ query.AgentAppearanceReader = (*AgentDefinitionLookup)(nil)
)

func NewAgentDefinitionLookup(db *xorm.Engine) *AgentDefinitionLookup {
	return &AgentDefinitionLookup{db: db}
}

func (r *AgentDefinitionLookup) FindForPurpose(ctx context.Context, input query.GetAgentDefinitionForPurpose) (string, error) {
	var row agentDefinitionDO
	found, err := r.db.Context(ctx).Cols("id").Where("scope_kind = ? AND scope_ref = ? AND purpose = ? AND deleted_at = 0", input.ScopeKind, input.ScopeReference, input.Purpose).Get(&row)
	if err != nil {
		return "", oops.With("operation", "agent.find_for_purpose").Wrap(err)
	}
	if !found {
		return "", domain.ErrAgentDefinitionNotFound
	}
	return row.ID, nil
}

// ListEmployeeAgents reads the employees' definitions for the purpose.
func (r *AgentDefinitionLookup) ListEmployeeAgents(ctx context.Context, purpose string, employeeIDs []string) (map[string]query.AgentAppearanceView, error) {
	views := make(map[string]query.AgentAppearanceView, len(employeeIDs))
	if len(employeeIDs) == 0 {
		return views, nil
	}
	var rows []agentDefinitionDO
	if err := r.db.Context(ctx).Cols("id", "scope_ref", "agent_name", "icon_glyph", "icon_color").Where("scope_kind = ? AND purpose = ? AND deleted_at = 0", "employee", purpose).In("scope_ref", employeeIDs).Find(&rows); err != nil {
		return nil, oops.With("operation", "agent.list_employee_agents").Wrap(err)
	}
	for _, row := range rows {
		views[row.ScopeRef] = appearanceView(row)
	}
	return views, nil
}

// ListWorkAgents reads the definitions of the Works.
func (r *AgentDefinitionLookup) ListWorkAgents(ctx context.Context, workIDs []string) (map[string]query.AgentAppearanceView, error) {
	views := make(map[string]query.AgentAppearanceView, len(workIDs))
	if len(workIDs) == 0 {
		return views, nil
	}
	var rows []struct {
		WorkID    string `xorm:"work_id"`
		AgentID   string `xorm:"agent_id"`
		AgentName string `xorm:"agent_name"`
		IconGlyph string `xorm:"icon_glyph"`
		IconColor string `xorm:"icon_color"`
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(workIDs)), ",")
	args := make([]any, len(workIDs))
	for index, id := range workIDs {
		args[index] = id
	}
	statement := `SELECT s.id AS work_id, a.id AS agent_id, a.agent_name, a.icon_glyph, a.icon_color
FROM agent_session s JOIN agent_definition a ON a.id = s.agent_id AND a.deleted_at = 0
WHERE s.deleted_at = 0 AND s.id IN (` + placeholders + `)`
	if err := r.db.Context(ctx).SQL(statement, args...).Find(&rows); err != nil {
		return nil, oops.With("operation", "agent.list_work_agents").Wrap(err)
	}
	for _, row := range rows {
		views[row.WorkID] = appearanceView(agentDefinitionDO{ID: row.AgentID, AgentName: row.AgentName, IconGlyph: row.IconGlyph, IconColor: row.IconColor})
	}
	return views, nil
}

func appearanceView(row agentDefinitionDO) query.AgentAppearanceView {
	return query.AgentAppearanceView{AgentID: row.ID, Appearance: domain.AgentAppearance{Name: row.AgentName, Icon: agentIconFromRecord(row.IconGlyph, row.IconColor)}}
}
