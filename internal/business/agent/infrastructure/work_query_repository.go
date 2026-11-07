package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
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
	WorkProgress          string  `xorm:"work_progress"`
	ModelConfigurationID  string  `xorm:"model_configuration_id"`
	ModelMachineLogin     bool    `xorm:"model_machine_login"`
	PermissionMode        string  `xorm:"permission_mode"`
	ComputerID            string  `xorm:"computer_id"`
	Capabilities          string  `xorm:"capabilities"`
	Version               int     `xorm:"version"`
	EndRequest            string  `xorm:"end_request"`
	InitialID             string  `xorm:"initial_id"`
	InitialData           string  `xorm:"initial_data"`
	InitialVersion        int     `xorm:"initial_version"`
	ContinuationVersion   int     `xorm:"continuation_version"`
	ContinuationRejection string  `xorm:"continuation_rejection"`
	ID                    string  `xorm:"id"`
	SubjectKind           *string `xorm:"subject_kind"`
	SubjectID             *string `xorm:"subject_id"`
	ProjectRef            string  `xorm:"project_ref"`
	AgentOwner            string  `xorm:"agent_owner"`
	StartedBy             string  `xorm:"started_by"`
	State                 uint8   `xorm:"state"`
	HarnessProcess        string  `xorm:"harness_process"`
	StartRequestID        string  `xorm:"start_request_id"`
	StopReason            string  `xorm:"stop_reason"`
	DisconnectedSince     int64   `xorm:"disconnected_since"`
	StopRequest           string  `xorm:"stop_request"`
	Failure               string  `xorm:"failure_reason"`
	CreatedAt             int64   `xorm:"created_at"`
	UpdatedAt             int64   `xorm:"updated_at"`
	ContinuationID        string  `xorm:"continuation_id"`
	ContinuationPending   bool    `xorm:"continuation_pending"`
	VisibilityPublic      bool    `xorm:"visibility_public"`
	ReachResumeDue        bool    `xorm:"reach_resume_due"`
	// ExecutionState is the lifecycle state of the Work's latest
	// AgentExecution; empty before its start's execution begins.
	ExecutionState string `xorm:"execution_state"`
}

// Read the lifecycle and continuation together. The work must not combine an
// old stopped snapshot with a separately observed completed Resume request.
// Bind the same identity on both tables: their different ID character sets must
// not turn the continuation primary-key lookup into a converted-column scan.
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
 s.visibility_public,s.reach_resume_due,
 COALESCE((SELECT r.id FROM agent_start_request r WHERE r.session_id=s.id AND r.canceled=0 AND r.deleted_at=0 ORDER BY r.created_at DESC, r.id DESC LIMIT 1),'') AS start_request_id,
 COALESCE(i.request_id,'') AS initial_id, COALESCE(i.request_data,'') AS initial_data, COALESCE(i.version,0) AS initial_version,
 COALESCE(c.version,0) AS continuation_version, COALESCE(c.rejection,'') AS continuation_rejection,
 COALESCE(c.request_id,'') AS continuation_id,COALESCE(c.pending,0) AS continuation_pending,
 COALESCE((SELECT e.state FROM execution_allocation e WHERE e.session_id=s.id ORDER BY e.created_at DESC, e.id DESC LIMIT 1),'') AS execution_state
 FROM agent_session s LEFT JOIN agent_session_continuation_request c ON c.id=? AND c.deleted_at=0
 LEFT JOIN agent_initial_execution_request i ON i.id=? AND i.deleted_at=0
 WHERE s.id=? AND s.deleted_at=0`, id, id, id).Get(&row)
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
	if row.InitialID != "" {
		initial, err := initialExecutionRequestFromDO(initialExecutionRequestDO{ID: row.ID, RequestID: row.InitialID, RequestData: row.InitialData, Version: row.InitialVersion})
		if err != nil {
			return result, err
		}
		var capabilities snapshotRecord
		if err := json.Unmarshal([]byte(row.Capabilities), &capabilities); err != nil {
			return result, oops.Wrap(err)
		}
		request := &query.WorkStartupRequest{RequestID: initial.RequestID, InitialMessage: initial.Input.Text, InitialThinkingLevel: initial.ThinkingLevel, StartupAgent: capabilities.StartupAgent}
		for _, plugin := range capabilities.Plugins {
			request.Plugins = append(request.Plugins, plugin.Release.Slug)
		}
		for _, selection := range initial.Selections {
			repository := query.WorkStartupRepository{RepositoryID: selection.RepositoryID, BaseBranch: selection.BaseBranch}
			for _, checkout := range initial.Checkouts {
				if checkout.RepoID == selection.RepositoryID {
					repository.Name, repository.BaseBranch = checkout.RepoName, checkout.BaseBranch
					break
				}
			}
			for _, outcome := range initial.CheckoutResults {
				if outcome.RepositoryID != selection.RepositoryID {
					continue
				}
				repository.CheckoutStatus, repository.CheckoutReason = "checked_out", ""
				if !outcome.Prepared {
					repository.CheckoutStatus, repository.CheckoutReason = "not_checked_out", outcome.Reason
				}
			}
			request.Repositories = append(request.Repositories, repository)
		}
		result.StartupRequest = request
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
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(), ResumePending: row.ContinuationPending, ResumeOnReconnection: row.ReachResumeDue}
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
		result.ResumePending = result.ResumePending || (state == domain.WorkStarting && !result.DeskWaiting && row.ContinuationID != "" && row.ContinuationID == row.StartRequestID)
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

func startupFromRows(row workViewRow, sandboxReady, recovering bool, executionUpdatedAt int64) (*query.StartupProgress, error) {
	var initial command.InitialExecutionRequest
	if row.InitialID != "" {
		var err error
		initial, err = initialExecutionRequestFromDO(initialExecutionRequestDO{ID: row.ID, RequestID: row.InitialID, RequestData: row.InitialData, Version: row.InitialVersion})
		if err != nil {
			return nil, err
		}
	}
	var process *harnessProcessRecord
	if err := json.Unmarshal([]byte(row.HarnessProcess), &process); err != nil {
		return nil, oops.Wrap(err)
	}
	f := query.StartupFacts{RequestID: initial.RequestID, Initial: initial.RequestID != "", State: string(sessionStates[row.State]), InputPrepared: initial.InputPrepared, InputBuffered: initial.InputBuffered, Rejected: initial.Rejected, Recovering: initial.RecoveryPending, Reason: initial.FailureReason, EndRequested: row.EndRequest != ""}
	f.InputPending = initial.RequestID != "" && !initial.InputBuffered
	if process != nil {
		f.RequestID = row.StartRequestID
		f.Admitted = true
		f.AgentReady = process.Ready
		// Native readiness necessarily followed environment and credential readiness.
		f.SandboxReady = sandboxReady || f.AgentReady
		f.Initial = row.StartRequestID == initial.RequestID
		// Rejected input stays in history. Its failure and unfinished preparation
		// do not belong to a later explicitly admitted start.
		if initial.Rejected && !f.Initial {
			f.InputPending, f.Rejected, f.Recovering = false, false, false
			f.Reason = ""
		}
		if row.Failure != "" {
			f.Reason = row.Failure
		}
	}
	// A Resume refused because its Managed Computer was unreachable starts
	// nothing and fails nothing: the Work stays Stopped and its Resume is owed
	// on reconnection (Agent model: Capacity rejection state, Retry on
	// reconnection), so the refusal leaves the earlier start's facts shown.
	unreachable := command.RejectedAsUnreachable(row.ContinuationRejection)
	if row.ContinuationID != "" && (row.ContinuationPending || row.ContinuationRejection != "") && !unreachable && (process == nil || row.ContinuationID != row.StartRequestID) {
		f.RequestID = row.ContinuationID
		f.Admitted = false
		f.AgentReady = false
		f.SandboxReady = false
		f.State = ""
		f.EndRequested = false
		f.Initial = false
		f.InputPending = initial.RequestID != "" && !initial.InputBuffered && !initial.Rejected
		f.Recovering = false
		f.Rejected = row.ContinuationRejection != ""
		f.Reason = row.ContinuationRejection
	}
	// Reconnection errors no longer describe startup once native readiness is
	// confirmed. Workspace and initial-input recovery belong to the initial receipt.
	if f.Admitted && !f.AgentReady {
		f.Recovering = recovering
	}
	// Source versions are monotone within a Work. A new start first bumps the
	// Work version, so an execution's progress, ordered by when its record last
	// changed (facts and technical progress alike), cannot regress order.
	f.Revision = fmt.Sprintf("%010d:%010d:%010d:%013d", row.Version, row.InitialVersion, row.ContinuationVersion, executionUpdatedAt)
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
