package infrastructure

import (
	"context"
	"strings"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The executions the recovery sweep and published changes concern, found by
// their indexed columns.

// ListOutstandingExecutions pages unretired executions and retired ones whose
// release is not confirmed. A released execution is history and is never
// listed.
func (r *ExecutionQueryRepository) ListOutstandingExecutions(ctx context.Context, afterID string, limit int) ([]string, error) {
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM (
 SELECT id FROM agent_execution WHERE deleted_at = 0 AND id > ?
 UNION
 SELECT id FROM agent_execution WHERE retired = 1 AND released_at = 0 AND id > ?
) AS outstanding ORDER BY id LIMIT ?`, afterID, afterID, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "list_outstanding_executions").Wrap(err)
	}
	return ids, nil
}

// ListExecutionsUsingAuthorization lists the unretired executions whose Work
// selected the authorization or reaches repositories through it.
func (r *ExecutionQueryRepository) ListExecutionsUsingAuthorization(ctx context.Context, authorizationID string) ([]string, error) {
	if strings.TrimSpace(authorizationID) == "" {
		return nil, nil
	}
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM agent_execution
 WHERE ? MEMBER OF(authorization_ids) AND deleted_at = 0`, authorizationID).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "list_executions_using_authorization").Wrap(err)
	}
	return ids, nil
}

// ListExecutionsReadingInputs lists the unretired Managed executions whose
// inputs sync recorded owner's File Space as their Work's input source. One
// that has not recorded its owner yet is due from its start.
func (r *ExecutionQueryRepository) ListExecutionsReadingInputs(ctx context.Context, owner string) ([]string, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, nil
	}
	var ids []string
	err := r.db.Context(ctx).SQL(`SELECT id FROM agent_execution
 WHERE input_owner = ? AND deleted_at = 0 AND realization = ?`, owner, string(domain.RealizationManaged)).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "list_executions_reading_inputs").Wrap(err)
	}
	return ids, nil
}
