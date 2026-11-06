package infrastructure

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// WorkAuthorizer applies Admit control requester to the Owners of the Work's
// definition without loading the Work. Callers delegate an Owner only after
// authorizing their outer request; Agent adds no Project rules.
type WorkAuthorizer struct{ db *xorm.Engine }

var _ access.WorkAuthorizer = (*WorkAuthorizer)(nil)

func NewWorkAuthorizer(db *xorm.Engine) *WorkAuthorizer { return &WorkAuthorizer{db: db} }

// owners reads the Owners of the Work's definition.
func (a *WorkAuthorizer) owners(ctx context.Context, workID string) ([]string, bool, error) {
	var rows []struct {
		Owners string `xorm:"owners"`
	}
	err := a.db.Context(ctx).SQL("SELECT d.owners FROM agent_session s JOIN agent_definition d ON d.id = s.agent_id AND d.deleted_at = 0 WHERE s.id = ? AND s.deleted_at = 0", workID).Find(&rows)
	if err != nil {
		return nil, false, oops.With("operation", "agent_work.owners").Wrap(err)
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	var owners []string
	if err := json.Unmarshal([]byte(rows[0].Owners), &owners); err != nil {
		return nil, false, oops.Wrap(err)
	}
	return owners, true, nil
}

func (a *WorkAuthorizer) AuthorizeWork(ctx context.Context, subject, id string) error {
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(id) == "" {
		return access.ErrAccessDenied
	}
	owners, found, err := a.owners(ctx, id)
	if err != nil {
		return err
	}
	if !found || domain.AdmitAgentOwner(owners, subject) != nil {
		return access.ErrAccessDenied
	}
	return nil
}
