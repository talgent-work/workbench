package infrastructure

import (
	"context"
	"slices"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ListUnclosedWorksReadingInputs pages the Works not Closed whose located
// inputs are the Directory, found by the inputs Directory key each Work
// records at creation.
func (r *WorkQueryRepository) ListUnclosedWorksReadingInputs(ctx context.Context, directory domain.Directory, afterID string, limit int) ([]string, error) {
	if !directory.Present() {
		return nil, nil
	}
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM agent_session
 WHERE inputs_directory = ? AND deleted_at = 0 AND state <> ? AND id > ?
 ORDER BY id LIMIT ?`, DirectoryKey(directory), slices.Index(sessionStates[:], domain.WorkClosed), afterID, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "agent.list_unclosed_works_reading_inputs").Wrap(err)
	}
	return ids, nil
}
