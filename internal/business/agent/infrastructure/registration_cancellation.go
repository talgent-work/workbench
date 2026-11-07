package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A canceled creation has no Work row to lock. Its original request's unique
// key also serializes initial registration, which rolls back its Work and input
// if this receipt won. No Work lifecycle or execution obligation is fabricated.
func (r *AgentWorkRepository) CancelWorkRegistration(ctx context.Context, request command.CancelWorkRegistration) (string, error) {
	scope, err := registrationScope(request.SubjectID, request.AgentID, request.WorkspaceReference, request.Subject, request.ProjectID)
	if err != nil {
		return "", err
	}
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return "", oops.Wrap(err)
	}
	defer tx.Rollback()
	now := r.now().UTC().UnixMilli()
	_, err = tx.Exec(`INSERT INTO agent_start_request
 (id, session_id, kind, canceled, creation_scope, version, created_at, updated_at, deleted_at)
 VALUES (?, '', '', 1, ?, 1, ?, ?, 0) ON DUPLICATE KEY UPDATE id = id`, request.RequestID, scope, now, now)
	if err != nil {
		return "", oops.With("operation", "agent.cancel_work_registration").Wrap(err)
	}
	var row startRequestDO
	found, err := tx.Where("id = ? AND deleted_at = 0", request.RequestID).ForUpdate().Get(&row)
	if err != nil {
		return "", oops.Wrap(err)
	}
	if !found {
		return "", command.ErrStartRequestConflict
	}
	if !row.Canceled {
		if row.Kind != string(query.StartKindStart) || row.SessionID == "" {
			return "", command.ErrStartRequestConflict
		}
		// No mutation for an admitted request; Application authorizes the Work
		// and verifies its original registration scope before returning its ID.
		return row.SessionID, nil
	}
	if row.SessionID != "" || row.CreationScope != scope {
		return "", command.ErrStartRequestConflict
	}
	return "", oops.Wrap(tx.Commit())
}

// Only correlation fields enter the digest, never plugin configuration or input.
func registrationScope(principal, agentID, workspace string, subject domain.WorkSubject, projectID string) (string, error) {
	data, err := json.Marshal(struct {
		Principal, Agent, Workspace string
		Subject                     domain.WorkSubject
		Project                     string
	}{principal, agentID, workspace, subject, projectID})
	if err != nil {
		return "", oops.Wrap(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
