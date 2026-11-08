package execution

import (
	"context"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// AgentExecutionRepository keeps every AgentExecution in agent_execution
// and its Plugin outcomes in execution_plugin_result. It writes the
// execution's facts under its optimistic version and leaves the technical
// columns to the execution environment adapter.
type AgentExecutionRepository struct {
	records
	now func() time.Time
}

var _ domain.AgentExecutionRepository = (*AgentExecutionRepository)(nil)

func NewAgentExecutionRepository(db *xorm.Engine, now func() time.Time) *AgentExecutionRepository {
	return &AgentExecutionRepository{records: records{db: db}, now: now}
}

// factColumns are the columns holding an execution's facts.
var factColumns = []string{"state", "start_admitted_at", "desk_id", "seated_at", "desk_left_at", "submitted_at", "creation_deadline", "creation_answered", "start_rejected", "sandbox_id", "lease_expires_at", "credentials_delivered", "preparation_ended", "process_id", "process_prepared", "process_started", "process_exited", "exit_code", "exit_reason", "exit_request_id", "exit_cancels_turn", "credentials_expire_at", "model_credential_digest", "credentials_withdrawn", "inputs_due", "inputs_synced_at", "start_failure", "abandoned", "environment_lost", "loss_reason", "retirement", "deleted_at", "released_at", "checkout_results", "request_json", "version", "updated_at"}

func (r *AgentExecutionRepository) Get(ctx context.Context, id string) (*domain.AgentExecution, error) {
	record, found, err := r.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, domain.ErrExecutionNotFound
	}
	return r.restore(ctx, record)
}

func (r *AgentExecutionRepository) Latest(ctx context.Context, workID string) (*domain.AgentExecution, error) {
	record, found, err := r.latest(ctx, workID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, domain.ErrExecutionNotFound
	}
	return r.restore(ctx, record)
}

// restore rebuilds the execution of a record with its Plugin outcomes.
func (r *AgentExecutionRepository) restore(ctx context.Context, record executionRecord) (*domain.AgentExecution, error) {
	execution, err := executionFromRecord(record)
	if err != nil {
		return nil, err
	}
	if execution.PluginResults, err = pluginResults(ctx, r.db, execution.ID); err != nil {
		return nil, err
	}
	return execution, nil
}

func (r *AgentExecutionRepository) Save(ctx context.Context, execution *domain.AgentExecution) error {
	if err := execution.Validate(); err != nil {
		return err
	}
	record, err := recordFromExecution(execution)
	if err != nil {
		return err
	}
	now := r.now().UnixMilli()
	record.UpdatedAt = now
	tx := r.db.NewSession()
	defer tx.Close()
	tx = tx.Context(ctx)
	if err := tx.Begin(); err != nil {
		return oops.With("operation", "save_execution", "execution_id", execution.ID).Wrap(err)
	}
	defer tx.Rollback()
	if execution.Version == 0 {
		err = insertExecution(tx, record, execution.Launch.AuthorizationSelection, now)
	} else {
		err = updateExecution(tx, record, execution.Version)
	}
	if err != nil {
		return err
	}
	if err := savePluginResults(tx, execution.ID, execution.PluginResults, now); err != nil {
		return err
	}
	return oops.With("operation", "commit_execution", "execution_id", execution.ID).Wrap(tx.Commit())
}

// updateExecution writes the execution's facts under its loaded version.
func updateExecution(tx *xorm.Session, record executionRecord, version int) error {
	record.Version = version + 1
	changed, err := tx.Cols(factColumns...).Where("id = ? AND version = ?", record.ID, version).Update(&record)
	if err != nil {
		return oops.With("operation", "save_execution", "execution_id", record.ID).Wrap(err)
	}
	if changed != 1 {
		return domain.ErrConcurrentModification
	}
	return nil
}

// insertExecution records a new execution with its technical columns empty;
// the unique unretired execution per Work refuses a second one.
func insertExecution(tx *xorm.Session, record executionRecord, selection domain.WorkAuthorizationSelection, now int64) error {
	authorizations, err := usedAuthorizations(selection)
	if err != nil {
		return err
	}
	record.Version, record.CreatedAt = 1, now
	record.AuthorizationIDs = authorizations
	record.ServiceAccessJSON, record.CredentialPlanJSON, record.SpecificationJSON = []byte("[]"), []byte("null"), []byte("null")
	_, err = tx.Insert(&record)
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		return domain.ErrExecutionAdmission
	}
	if err != nil {
		return oops.With("operation", "insert_execution", "execution_id", record.ID).Wrap(err)
	}
	return nil
}

func recordFromExecution(e *domain.AgentExecution) (executionRecord, error) {
	launch, err := encodeLaunch(e.Launch)
	if err != nil {
		return executionRecord{}, err
	}
	checkouts, err := encodeCheckoutResults(e.CheckoutResults)
	if err != nil {
		return executionRecord{}, err
	}
	return executionRecord{
		CheckoutResults: checkouts,
		ID:              e.ID, SessionID: e.WorkID, Realization: string(e.Realization), RequestJSON: launch,
		State: string(e.State), StartAdmittedAt: millis(e.StartAdmittedAt), DeskID: e.Desk.ID, SeatedAt: millis(e.Desk.SeatedAt), DeskLeftAt: millis(e.Desk.LeftAt),
		SubmittedAt: millis(e.Creation.SubmittedAt), CreationDeadline: millis(e.Creation.Deadline), CreationAnswered: e.Creation.Answered, StartRejected: e.Outcome.Rejected, SandboxID: e.Environment, LeaseExpiresAt: millis(e.Lease),
		CredentialsDelivered: e.Credentials.Delivered, PreparationEnded: e.Process.PreparationEnded,
		ProcessID: e.Process.ID, ProcessPrepared: e.Process.Prepared, ProcessStarted: e.Process.Started, ProcessExited: e.Process.Exited, ExitCode: e.Process.Exit.ExitCode, ExitReason: e.Process.Exit.Reason,
		ExitRequestID: e.ExitRequest.StopRequestID, ExitCancels: e.ExitRequest.CancelWaitingTurn,
		CredentialsExpireAt: millis(e.Credentials.ExpiresAt), ModelCredentialDigest: e.Credentials.Digest, CredentialsWithdrawn: e.Credentials.Withdrawn,
		InputsDue: e.Inputs.Due, InputsSyncedAt: millis(e.Inputs.SyncedAt),
		StartFailure: e.Outcome.StartFailure, Abandoned: e.Outcome.Abandoned, EnvironmentLost: e.Outcome.Lost, LossReason: e.Outcome.LossReason,
		Retirement: string(e.Retirement), DeletedAt: millis(e.RetiredAt), ReleasedAt: millis(e.ReleasedAt),
	}, nil
}

func executionFromRecord(r executionRecord) (*domain.AgentExecution, error) {
	launch, err := decodeStartRequest(r.RequestJSON)
	if err != nil {
		return nil, oops.With("operation", "decode_execution_launch", "execution_id", r.ID).Wrap(err)
	}
	checkouts, err := decodeCheckoutResults(r.CheckoutResults)
	if err != nil {
		return nil, oops.With("operation", "decode_execution_checkouts", "execution_id", r.ID).Wrap(err)
	}
	execution := &domain.AgentExecution{
		CheckoutResults: checkouts,
		ID:              r.ID, WorkID: r.SessionID, StartAdmittedAt: fromMillis(r.StartAdmittedAt), Realization: domain.ExecutionRealization(r.Realization),
		Desk:        domain.ExecutionDesk{ID: r.DeskID, SeatedAt: fromMillis(r.SeatedAt), LeftAt: fromMillis(r.DeskLeftAt)},
		Launch:      launch,
		Creation:    domain.CreationSubmission{SubmittedAt: fromMillis(r.SubmittedAt), Deadline: fromMillis(r.CreationDeadline), Answered: r.CreationAnswered},
		Environment: r.SandboxID,
		Lease:       fromMillis(r.LeaseExpiresAt),
		Process: domain.LaunchedProcess{ID: r.ProcessID, PreparationEnded: r.PreparationEnded, Prepared: r.ProcessPrepared, Started: r.ProcessStarted, Exited: r.ProcessExited,
			Exit: domain.HarnessExit{ExitCode: r.ExitCode, Reason: r.ExitReason}},
		ExitRequest: domain.ExitRequest{StopRequestID: r.ExitRequestID, CancelWaitingTurn: r.ExitCancels},
		Credentials: domain.CredentialDelivery{Delivered: r.CredentialsDelivered, Digest: r.ModelCredentialDigest, ExpiresAt: fromMillis(r.CredentialsExpireAt), Withdrawn: r.CredentialsWithdrawn},
		Inputs:      domain.InputSync{Due: r.InputsDue, SyncedAt: fromMillis(r.InputsSyncedAt)},
		Outcome:     domain.ExecutionOutcome{StartFailure: r.StartFailure, Rejected: r.StartRejected, Abandoned: r.Abandoned, Lost: r.EnvironmentLost, LossReason: r.LossReason},
		State:       domain.ExecutionState(r.State), Retirement: domain.RetirementCause(r.Retirement),
		RetiredAt: fromMillis(r.DeletedAt), ReleasedAt: fromMillis(r.ReleasedAt),
		Events: domain.NewEventCollection(), Version: r.Version,
	}
	if err := execution.Validate(); err != nil {
		return nil, oops.With("execution_id", r.ID).Wrap(err)
	}
	return execution, nil
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
