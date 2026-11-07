package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

type WorkQueryRepository struct {
	db *xorm.Engine
}

var _ query.WorkQueryRepository = (*WorkQueryRepository)(nil)

func NewWorkQueryRepository(db *xorm.Engine) *WorkQueryRepository {
	return &WorkQueryRepository{db: db}
}

type workViewRow struct {
	WorkProgress         string `xorm:"work_progress"`
	ModelConfigurationID string `xorm:"model_configuration_id"`
	ModelMachineLogin    bool   `xorm:"model_machine_login"`
	PermissionMode       string `xorm:"permission_mode"`
	ComputerID           string `xorm:"computer_id"`
	Capabilities         string `xorm:"capabilities"`
	Version              int    `xorm:"version"`
	EndRequest           string `xorm:"end_request"`
	// CreationRequestID is the request identity that created the Work.
	CreationRequestID string `xorm:"creation_request_id"`
	// InitialTaskInput is the Work's initial task input not handed off yet.
	InitialTaskInput    string  `xorm:"initial_task_input"`
	ThinkingLevel       string  `xorm:"thinking_level"`
	RepositorySelection string  `xorm:"repository_selection"`
	RepositoryCheckouts string  `xorm:"repository_checkouts"`
	ID                  string  `xorm:"id"`
	SubjectKind         *string `xorm:"subject_kind"`
	SubjectID           *string `xorm:"subject_id"`
	ProjectRef          string  `xorm:"project_ref"`
	AgentOwner          string  `xorm:"agent_owner"`
	StartedBy           string  `xorm:"started_by"`
	State               uint8   `xorm:"state"`
	HarnessProcess      string  `xorm:"harness_process"`
	StartRequestID      string  `xorm:"start_request_id"`
	StopReason          string  `xorm:"stop_reason"`
	DisconnectedSince   int64   `xorm:"disconnected_since"`
	StopRequest         string  `xorm:"stop_request"`
	Failure             string  `xorm:"failure_reason"`
	CreatedAt           int64   `xorm:"created_at"`
	UpdatedAt           int64   `xorm:"updated_at"`
	VisibilityPublic    bool    `xorm:"visibility_public"`
	ReachResumeDue      bool    `xorm:"reach_resume_due"`
	// ExecutionState is the lifecycle state of the Work's latest
	// AgentExecution; empty before its start's execution begins.
	ExecutionState string `xorm:"execution_state"`
}

// GetWorkView reads the Work's lifecycle, its start requests and its current
// execution in one transaction, so the view never combines facts of different
// starts.
func (r *WorkQueryRepository) GetWorkView(ctx context.Context, id string) (query.WorkView, error) {
	var row workViewRow
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return query.WorkView{}, oops.Wrap(err)
	}
	defer tx.Rollback()
	// The latest admitted start request identifies which request started the
	// Work's current Harness process; it is correlation, not a process identity.
	found, err := tx.SQL(`SELECT s.id,s.model_configuration_id,s.model_machine_login,s.permission_mode,s.computer_id,s.capabilities,s.subject_kind,s.subject_id,s.project_ref,
 `+agentOwnerColumn+` AS agent_owner,s.started_by,s.state,s.harness_process,s.work_progress,
 s.stop_reason,s.disconnected_since,s.stop_request,s.failure_reason,s.created_at,s.updated_at,s.version,s.end_request,
 s.visibility_public,s.reach_resume_due,s.initial_task_input,s.thinking_level,s.repository_selection,s.repository_checkouts,
 COALESCE((SELECT r.id FROM agent_start_request r WHERE r.session_id=s.id AND r.canceled=0 AND r.deleted_at=0 ORDER BY r.created_at DESC, r.id DESC LIMIT 1),'') AS start_request_id,
 COALESCE((SELECT r.id FROM agent_start_request r WHERE r.session_id=s.id AND r.kind=? AND r.canceled=0 AND r.deleted_at=0 ORDER BY r.created_at, r.id LIMIT 1),'') AS creation_request_id,
 COALESCE((SELECT e.state FROM execution_allocation e WHERE e.session_id=s.id ORDER BY e.created_at DESC, e.id DESC LIMIT 1),'') AS execution_state
 FROM agent_session s WHERE s.id=? AND s.deleted_at=0`, string(query.StartKindStart), id).Get(&row)
	if err != nil {
		return query.WorkView{}, oops.With("operation", "agent.work_session").Wrap(err)
	}
	if !found {
		return query.WorkView{}, domain.ErrWorkNotFound
	}
	result, err := workViewFromRow(row)
	if err != nil {
		return result, err
	}
	var harness struct {
		Sandbox bool `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(row.Capabilities), &harness); err != nil {
		return result, oops.Wrap(err)
	}
	result.Sandbox = harness.Sandbox
	if result.WorkShares, err = workShares(tx, id); err != nil {
		return result, err
	}
	var allocation struct {
		Ready, Recovering bool
		UpdatedAt         int64
	}
	if row.HarnessProcess != "" && row.HarnessProcess != "null" {
		_, err = tx.SQL("SELECT sandbox_ready AS ready,preparation_recovering AS recovering,updated_at FROM execution_allocation WHERE session_id=? AND deleted_at=0 ORDER BY created_at DESC, id DESC LIMIT 1", id).Get(&allocation)
		if err != nil {
			return result, oops.Wrap(err)
		}
	}
	result.StartupProgress, err = startupFromRows(row, allocation.Ready, allocation.Recovering, allocation.UpdatedAt)
	if err != nil {
		return result, err
	}
	if result.StartupRequest, err = startupRequestFromRow(row); err != nil {
		return result, err
	}
	return result, oops.Wrap(tx.Commit())
}

func workViewFromRow(row workViewRow) (query.WorkView, error) {
	if int(row.State) >= len(sessionStates) {
		return query.WorkView{}, domain.ErrInvalidWork
	}
	var process *harnessProcessRecord
	if err := json.Unmarshal([]byte(row.HarnessProcess), &process); err != nil {
		return query.WorkView{}, oops.Wrap(err)
	}
	result := query.WorkView{Version: row.Version, WorkModel: domain.WorkModelSelection{ConfigurationID: row.ModelConfigurationID, MachineLogin: row.ModelMachineLogin}, WorkID: row.ID, Subject: subjectFromColumns(row.SubjectKind, row.SubjectID), ProjectID: row.ProjectRef, AgentOwner: row.AgentOwner, StartedBy: row.StartedBy, Visibility: domain.WorkVisibility{Public: row.VisibilityPublic},
		PermissionMode: domain.PermissionMode(row.PermissionMode), ComputerID: row.ComputerID,
		StateReason: row.StopReason, StopRequestID: row.StopRequest, DiagnosticReason: row.Failure, DisconnectedSince: fromUnixMillis(row.DisconnectedSince),
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(), ResumeOnReconnection: row.ReachResumeDue}
	progress, err := decodeWorkProgress(row.WorkProgress)
	if err != nil {
		return query.WorkView{}, err
	}
	if progress != nil {
		result.Progress = &query.WorkProgress{UpdatedAt: progress.UpdatedAt, Items: make([]query.WorkProgressItem, len(progress.Items))}
		for i, item := range progress.Items {
			result.Progress.Items[i] = query.WorkProgressItem{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm}
		}
	}
	state := sessionStates[row.State]
	if state == "" {
		return query.WorkView{}, domain.ErrInvalidWork
	}
	result.State = string(state)
	if process != nil {
		result.Waiting = process.waiting()
		for _, agent := range process.SubAgents {
			result.SubAgents = append(result.SubAgents, query.SubAgent{ID: agent.ID, Type: agent.Type, Task: agent.Task, Status: agent.Status})
		}
		// A Resume waiting for room is admitted; it is withdrawn, not pending.
		// The start's execution holds its Desk's seating once it began.
		queued := row.ExecutionState == "" || row.ExecutionState == string(domain.ExecutionQueued)
		result.DeskWaiting = state == domain.WorkStarting && process.DeskWaiting && queued
		// A Resume or Restart admitted as the current start awaits readiness.
		result.ResumePending = state == domain.WorkStarting && !result.DeskWaiting && row.StartRequestID != "" && row.StartRequestID != row.CreationRequestID
	}
	switch state {
	case domain.WorkFailed:
		result.State, result.StateReason = "failed", "runtime_error"
	case domain.WorkClosing:
		result.ArchivePending = true
		if row.Failure != "" {
			result.StateReason = "runtime_error"
		}
	}
	return result, nil
}

// startupRequestFromRow projects the start the Work was created with: its
// initial task input while the Work still holds it, its thinking level,
// capability identities and each selected repository with its checkout
// outcome once recorded. A Work without a creation request has none.
func startupRequestFromRow(row workViewRow) (*query.WorkStartupRequest, error) {
	if row.CreationRequestID == "" {
		return nil, nil
	}
	initial, err := decodeInitialTaskInput(row.InitialTaskInput)
	if err != nil {
		return nil, err
	}
	var capabilities snapshotRecord
	if err := json.Unmarshal([]byte(row.Capabilities), &capabilities); err != nil {
		return nil, oops.Wrap(err)
	}
	selection, err := decodeWorkRepositories(row.RepositorySelection)
	if err != nil {
		return nil, err
	}
	checkouts, err := decodeRepositoryCheckouts(row.RepositoryCheckouts)
	if err != nil {
		return nil, err
	}
	request := &query.WorkStartupRequest{RequestID: row.CreationRequestID, InitialThinkingLevel: row.ThinkingLevel, StartupAgent: capabilities.StartupAgent}
	if initial != nil {
		request.InitialMessage = initial.Text
	}
	for _, plugin := range capabilities.Plugins {
		request.Plugins = append(request.Plugins, plugin.Release.Slug)
	}
	for _, selected := range selection.Repositories {
		repository := query.WorkStartupRepository{RepositoryID: selected.RepoID, Name: selected.RepoName, BaseBranch: selected.BaseBranch}
		if i := slices.IndexFunc(checkouts, func(c domain.RepositoryCheckout) bool { return c.RepositoryID == selected.RepoID }); i >= 0 {
			repository.CheckoutStatus, repository.CheckoutReason = "checked_out", ""
			if !checkouts[i].CheckedOut {
				repository.CheckoutStatus, repository.CheckoutReason = "not_checked_out", checkouts[i].Reason
			}
		}
		request.Repositories = append(request.Repositories, repository)
	}
	return request, nil
}

// startupFromRows projects the startup progress of the Work's current start
// from its facts: the first start prepares the workspace, every selected
// repository ending with an outcome, and hands off the initial task input.
func startupFromRows(row workViewRow, sandboxReady, recovering bool, executionUpdatedAt int64) (*query.StartupProgress, error) {
	var process *harnessProcessRecord
	if err := json.Unmarshal([]byte(row.HarnessProcess), &process); err != nil {
		return nil, oops.Wrap(err)
	}
	selection, err := decodeWorkRepositories(row.RepositorySelection)
	if err != nil {
		return nil, err
	}
	checkouts, err := decodeRepositoryCheckouts(row.RepositoryCheckouts)
	if err != nil {
		return nil, err
	}
	created := row.CreationRequestID != ""
	// The initial task input is buffered once the Work no longer holds it.
	inputBuffered := created && row.InitialTaskInput == ""
	f := query.StartupFacts{RequestID: row.CreationRequestID, Initial: created, State: string(sessionStates[row.State]),
		InputPrepared: len(checkouts) >= len(selection.Repositories), InputBuffered: inputBuffered, EndRequested: row.EndRequest != ""}
	f.InputPending = created && !inputBuffered
	if process != nil {
		f.RequestID = row.StartRequestID
		f.Admitted = true
		f.AgentReady = process.Ready
		// Native readiness necessarily followed environment and credential readiness.
		f.SandboxReady = sandboxReady || f.AgentReady
		f.Initial = created && row.StartRequestID == row.CreationRequestID
		f.Reason = row.Failure
	}
	if f.Admitted && !f.AgentReady {
		f.Recovering = recovering
	}
	// Source versions are monotone within a Work. A new start first bumps the
	// Work version, so an execution's progress, ordered by when its record last
	// changed (facts and technical progress alike), cannot regress order.
	f.Revision = fmt.Sprintf("%010d:%013d", row.Version, executionUpdatedAt)
	return query.ProjectStartup(f), nil
}

func (r *WorkQueryRepository) GetWorkOrigin(ctx context.Context, sessionID string) (query.WorkOrigin, error) {
	var row struct {
		SessionID   string  `xorm:"session_id"`
		AgentID     string  `xorm:"agent_id"`
		Purpose     string  `xorm:"purpose"`
		SubjectKind *string `xorm:"subject_kind"`
		SubjectID   *string `xorm:"subject_id"`
		ProjectRef  string  `xorm:"project_ref"`
		StartedBy   string  `xorm:"started_by"`
		CreatedAt   int64   `xorm:"created_at"`
	}
	found, err := r.db.Context(ctx).SQL(`SELECT s.id AS session_id, s.agent_id, a.purpose, s.subject_kind, s.subject_id, s.project_ref, s.started_by, s.created_at
FROM agent_session s JOIN agent_definition a ON a.id = s.agent_id AND a.deleted_at = 0
WHERE s.id = ? AND s.deleted_at = 0`, sessionID).Get(&row)
	if err != nil {
		return query.WorkOrigin{}, oops.With("operation", "agent.get_session_origin").With("session_id", sessionID).Wrap(err)
	}
	if !found {
		return query.WorkOrigin{}, domain.ErrWorkNotFound
	}
	return query.WorkOrigin{WorkID: row.SessionID, AgentID: row.AgentID, Purpose: row.Purpose, Subject: subjectFromColumns(row.SubjectKind, row.SubjectID), ProjectID: row.ProjectRef, StartedBy: row.StartedBy, StartedAt: time.UnixMilli(row.CreatedAt).UTC()}, nil
}

// ListWorkOrigins reads the origins of the Works in one query; a Work that
// does not exist is absent from the result.
func (r *WorkQueryRepository) ListWorkOrigins(ctx context.Context, sessionIDs []string) (map[string]query.WorkOrigin, error) {
	out := make(map[string]query.WorkOrigin, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SessionID   string  `xorm:"session_id"`
		AgentID     string  `xorm:"agent_id"`
		Purpose     string  `xorm:"purpose"`
		SubjectKind *string `xorm:"subject_kind"`
		SubjectID   *string `xorm:"subject_id"`
		ProjectRef  string  `xorm:"project_ref"`
		StartedBy   string  `xorm:"started_by"`
		CreatedAt   int64   `xorm:"created_at"`
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sessionIDs)), ",")
	args := make([]any, len(sessionIDs))
	for index, id := range sessionIDs {
		args[index] = id
	}
	statement := `SELECT s.id AS session_id, s.agent_id, a.purpose, s.subject_kind, s.subject_id, s.project_ref, s.started_by, s.created_at
FROM agent_session s JOIN agent_definition a ON a.id = s.agent_id AND a.deleted_at = 0
WHERE s.deleted_at = 0 AND s.id IN (` + placeholders + `)`
	if err := r.db.Context(ctx).SQL(statement, args...).Find(&rows); err != nil {
		return nil, oops.With("operation", "agent.list_session_origins").Wrap(err)
	}
	for _, row := range rows {
		out[row.SessionID] = query.WorkOrigin{WorkID: row.SessionID, AgentID: row.AgentID, Purpose: row.Purpose, Subject: subjectFromColumns(row.SubjectKind, row.SubjectID), ProjectID: row.ProjectRef, StartedBy: row.StartedBy, StartedAt: time.UnixMilli(row.CreatedAt).UTC()}
	}
	return out, nil
}

// recentWorkScan bounds how many of the owner's latest Works are inspected for
// repository selections.
const recentWorkScan = 50

func (r *WorkQueryRepository) ListRecentRepositoryIDs(ctx context.Context, owner string) ([]string, error) {
	var rows []struct {
		RepositorySelection string `xorm:"repository_selection"`
	}
	err := r.db.Context(ctx).Table("agent_session").Cols("repository_selection").
		Where("home_owner_id = ? AND deleted_at = 0", owner).Desc("created_at").Desc("id").Limit(recentWorkScan).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "agent.recent_repositories").Wrap(err)
	}
	seen := map[string]bool{}
	var ids []string
	for _, row := range rows {
		selection, err := decodeWorkRepositories(row.RepositorySelection)
		if err != nil {
			return nil, err
		}
		for _, repository := range selection.Repositories {
			if !seen[repository.RepoID] {
				seen[repository.RepoID] = true
				ids = append(ids, repository.RepoID)
			}
		}
	}
	return ids, nil
}
