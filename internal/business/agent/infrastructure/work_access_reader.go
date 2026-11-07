package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"xorm.io/xorm"
)

// WorkAccessReader reads the facts Work access is decided from, without
// loading the Work.
type WorkAccessReader struct{ db *xorm.Engine }

var _ access.WorkAccessReader = (*WorkAccessReader)(nil)

func NewWorkAccessReader(db *xorm.Engine) *WorkAccessReader { return &WorkAccessReader{db: db} }

// WorkOwners reads the Owners of the Work's definition.
func (r *WorkAccessReader) WorkOwners(ctx context.Context, workID string) ([]string, bool, error) {
	var rows []struct {
		Owners string `xorm:"owners"`
	}
	err := r.db.Context(ctx).SQL("SELECT d.owners FROM agent_session s JOIN agent_definition d ON d.id = s.agent_id AND d.deleted_at = 0 WHERE s.id = ? AND s.deleted_at = 0", workID).Find(&rows)
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
