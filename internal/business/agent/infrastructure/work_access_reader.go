package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkOwners reads the Owners of the Work's definition that it still admits,
// the facts Work access is decided from, without loading the Work.
func (r *WorkQueryRepository) WorkOwners(ctx context.Context, workID string) ([]string, bool, error) {
	var rows []struct {
		Owners             string `xorm:"owners"`
		ApplicationRemoved bool   `xorm:"application_removed"`
	}
	err := r.db.Context(ctx).SQL("SELECT d.owners, d.application_removed FROM agent_session s JOIN agent_definition d ON d.id = s.agent_id AND d.deleted_at = 0 WHERE s.id = ? AND s.deleted_at = 0", workID).Find(&rows)
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
	return domain.AdmittedOwners(owners, rows[0].ApplicationRemoved), true, nil
}
