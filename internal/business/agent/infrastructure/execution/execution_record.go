package execution

import (
	"context"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// allocationRecord is the persistence model of an AgentExecution
// (execution_allocation). The AgentExecution repository alone writes the
// execution's facts and its version; the execution environment adapter
// writes only the technical columns that realize its steps: the environment
// specification, launch commands, credential plan, service access, the
// preparation process and checkout plan, and the Managed input owner.
type allocationRecord struct {
	ID        string `xorm:"pk 'id'"`
	SessionID string `xorm:"'session_id'"`
	// Realization names the environment authority holding the execution's
	// environment, fixed at its beginning (environment_authority.go).
	Realization string `xorm:"'realization'"`
	// RequestHash fingerprints RequestJSON, the execution's
	// LaunchSpecification.
	RequestHash string `xorm:"'request_hash'"`
	RequestJSON []byte `xorm:"'request_json'"`

	// The execution's facts.
	State           string `xorm:"'state'"`
	StartAdmittedAt int64  `xorm:"'start_admitted_at'"`
	DeskID          string `xorm:"'desk_id'"`
	SeatedAt        int64  `xorm:"'seated_at'"`
	DeskLeftAt      int64  `xorm:"'desk_left_at'"`
	Submitted       bool   `xorm:"'submitted'"`
	// StartRejected records that the creation was definitely refused, or
	// failed, while nothing was bound.
	StartRejected bool `xorm:"start_rejected"`
	// SandboxID is the bound environment: a Cloud's sandbox identity, or on a
	// Managed Computer the execution's own identity, standing for the Work's
	// opened directory.
	SandboxID string `xorm:"'sandbox_id'"`
	// LeaseExpiresAt is the bound sandbox's expiry, in Unix milliseconds, as
	// OpenSandbox last returned it; zero while none is known.
	LeaseExpiresAt int64 `xorm:"'lease_expires_at'"`
	// SandboxReady records the first credential delivery into the bound
	// environment.
	SandboxReady bool `xorm:"sandbox_ready"`
	// EnvironmentPrepared records that the preparation process ended and its
	// Plugin and checkout outcomes were recorded.
	EnvironmentPrepared bool   `xorm:"'plugins_prepared'"`
	ProcessID           string `xorm:"'process_id'"`
	ProcessPrepared     bool   `xorm:"'process_prepared'"`
	ProcessStarted      bool   `xorm:"'process_started'"`
	// ProcessExited records that Agent acknowledged the exit fact of ProcessID.
	ProcessExited bool   `xorm:"'process_exited'"`
	ExitCode      int    `xorm:"'exit_code'"`
	ExitReason    string `xorm:"'exit_reason'"`
	ExitRequestID string `xorm:"'exit_request_id'"`
	ExitCancels   bool   `xorm:"'exit_cancels_turn'"`
	// CredentialsExpireAt is the earliest declared expiry, in Unix
	// milliseconds, of the credentials last delivered to the Vault; zero when
	// none of them expires.
	CredentialsExpireAt int64 `xorm:"'credentials_expire_at'"`
	// ModelCredentialDigest identifies the model key last delivered.
	ModelCredentialDigest string `xorm:"'model_credential_digest'"`
	CredentialsWithdrawn  bool   `xorm:"'credentials_withdrawn'"`
	// InputsDue counts the input changes a Managed execution's machine has
	// not been synced with; zero when it holds the current inputs.
	InputsDue int64 `xorm:"'inputs_due'"`
	// InputsSyncedAt is when, in Unix milliseconds, a Managed execution's
	// inputs were last synced; zero before the first.
	InputsSyncedAt  int64  `xorm:"'inputs_synced_at'"`
	StartFailure    string `xorm:"'start_failure'"`
	Abandoned       bool   `xorm:"'abandoned'"`
	EnvironmentLost bool   `xorm:"'environment_lost'"`
	LossReason      string `xorm:"'loss_reason'"`
	Retirement      string `xorm:"'retirement'"`
	// DeletedAt is when, in Unix milliseconds, the execution retired; zero
	// while it wants its run.
	DeletedAt int64 `xorm:"'deleted_at'"`
	// ReleasedAt is when, in Unix milliseconds, a retired execution's release
	// was confirmed; zero while its release is outstanding.
	ReleasedAt int64 `xorm:"'released_at'"`
	// CheckoutResults is the JSON of the outcomes of the repositories the
	// run checked out; JSON null until its checkout ended.
	CheckoutResults []byte `xorm:"'checkout_results'"`
	// AuthorizationIDs are the Provider Authorizations the Work selected; its
	// repository and service access goes through them, so a credential change
	// finds its Works.
	AuthorizationIDs []byte `xorm:"'authorization_ids'"`
	Version          int    `xorm:"'version'"`
	CreatedAt        int64  `xorm:"'created_at'"`
	UpdatedAt        int64  `xorm:"'updated_at'"`

	// Technical columns of the execution environment adapter.
	ServiceAccessJSON  []byte `xorm:"'service_access_json'"`
	CredentialPlanJSON []byte `xorm:"'credential_plan_json'"`
	SpecificationJSON  []byte `xorm:"'specification_json'"`
	PreparationCommand string `xorm:"'preparation_command'"`
	// PreparationProcessID is the preparation process the adapter started.
	PreparationProcessID string `xorm:"'preparation_process_id'"`
	// CheckoutPlan is the plan this start's preparation checks out, fixed
	// when the preparation process is created; JSON null for none.
	CheckoutPlan    []byte `xorm:"'checkout_plan'"`
	LaunchCommand   string `xorm:"'launch_command'"`
	NativeSessionID string `xorm:"'native_session_id'"`
	// InputOwner is the File Space owner of the Work's input source, recorded
	// by its inputs sync; a change in that space finds the execution by it.
	InputOwner string `xorm:"'input_owner'"`
	// HandlesReleased records that a Managed execution's process handles were
	// deleted on its machine.
	HandlesReleased       bool `xorm:"'handles_released'"`
	PreparationRecovering bool `xorm:"preparation_recovering"`
	// AllocationLabel equals the sandbox's allocation label, so an observed
	// sandbox finds its execution without reading every one. MySQL derives it
	// from the identity; it is never written.
	AllocationLabel string `xorm:"<- 'allocation_label'"`
}

func (*allocationRecord) TableName() string { return "execution_allocation" }

// specified reports whether the environment's specification was fixed.
func (r allocationRecord) specified() bool {
	return len(r.SpecificationJSON) > 0 && string(r.SpecificationJSON) != "null"
}

// records reads execution records.
type records struct{ db *xorm.Engine }

func (r records) get(ctx context.Context, id string) (allocationRecord, bool, error) {
	var record allocationRecord
	found, err := r.db.Context(ctx).Where("id = ?", id).Get(&record)
	if err != nil {
		return record, false, oops.With("operation", "get_execution").Wrap(err)
	}
	return record, found, nil
}

// live returns the Work's one unretired execution.
func (r records) live(ctx context.Context, workID string) (allocationRecord, bool, error) {
	var record allocationRecord
	found, err := r.db.Context(ctx).Where("session_id = ? AND deleted_at = 0", workID).Get(&record)
	if err != nil {
		return record, false, oops.With("operation", "get_live_execution").Wrap(err)
	}
	return record, found, nil
}

// latest returns the Work's most recently begun execution.
func (r records) latest(ctx context.Context, workID string) (allocationRecord, bool, error) {
	var record allocationRecord
	found, err := r.db.Context(ctx).Where("session_id = ?", workID).Desc("created_at", "id").Get(&record)
	if err != nil {
		return record, false, oops.With("operation", "get_latest_execution").Wrap(err)
	}
	return record, found, nil
}

// forSandbox returns the execution whose label an observed sandbox carries,
// retired ones included.
func (r records) forSandbox(ctx context.Context, label string) (string, bool, error) {
	if label == "" {
		return "", false, nil
	}
	var ids []string
	if err := r.db.Context(ctx).Table("execution_allocation").Cols("id").Where("allocation_label = ?", label).Limit(2).Find(&ids); err != nil {
		return "", false, oops.With("operation", "find_sandbox_execution").Wrap(err)
	}
	if len(ids) != 1 {
		// Absence, or a label that does not identify exactly one execution,
		// cannot prove ownership.
		return "", false, nil
	}
	return ids[0], true, nil
}

// current rereads the execution's record, so a step continues with the
// technical columns another step wrote meanwhile. Whether the execution still
// admits the step is the holding check's answer, never the record's.
func (r records) current(ctx context.Context, record allocationRecord) (allocationRecord, error) {
	current, found, err := r.get(ctx, record.ID)
	if err != nil {
		return allocationRecord{}, err
	}
	if !found {
		return allocationRecord{}, domain.ErrExecutionNotFound
	}
	return current, nil
}
