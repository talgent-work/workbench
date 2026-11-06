package execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// Each external attempt leaves time for durable bookkeeping and other records.
const reconciliationAttemptTimeout = 5 * time.Second

type allocationRecord struct {
	StartRejected         bool   `xorm:"start_rejected"`
	SandboxReady          bool   `xorm:"sandbox_ready"`
	PreparationRecovering bool   `xorm:"preparation_recovering"`
	ID                    string `xorm:"pk 'id'"`
	SessionID             string `xorm:"'session_id'"`
	RequestHash           string `xorm:"'request_hash'"`
	RequestJSON           []byte `xorm:"'request_json'"`
	ServiceAccessJSON     []byte `xorm:"'service_access_json'"`
	CredentialPlanJSON    []byte `xorm:"'credential_plan_json'"`
	SpecificationJSON     []byte `xorm:"'specification_json'"`
	// Realization names the environment authority holding the allocation's
	// environment, fixed at registration (environment_authority.go).
	Realization          string `xorm:"'realization'"`
	PreparationCommand   string `xorm:"'preparation_command'"`
	PreparationProcessID string `xorm:"'preparation_process_id'"`
	// CheckoutPlan is the plan this start's preparation checks out, fixed
	// when the preparation process is created; JSON null for none.
	CheckoutPlan []byte `xorm:"'checkout_plan'"`
	// EnvironmentPrepared records that the preparation process finished and its
	// Plugin and checkout outcomes were recorded.
	EnvironmentPrepared bool   `xorm:"'plugins_prepared'"`
	LaunchCommand       string `xorm:"'launch_command'"`
	NativeSessionID     string `xorm:"'native_session_id'"`
	ProcessID           string `xorm:"'process_id'"`
	// ProcessExited records that Agent acknowledged the exit fact of ProcessID.
	ProcessExited bool `xorm:"'process_exited'"`
	// CredentialsExpireAt is the earliest declared expiry, in Unix
	// milliseconds, of the credentials last delivered to the Vault; zero when
	// none of them expires.
	CredentialsExpireAt int64 `xorm:"'credentials_expire_at'"`
	// ModelCredentialDigest identifies the model key last delivered.
	ModelCredentialDigest string `xorm:"'model_credential_digest'"`
	// InputsDue counts the input changes a Managed allocation's machine has
	// not been synced with; zero when it holds the current inputs.
	InputsDue int64 `xorm:"'inputs_due'"`
	// InputOwner is the File Space owner of the Work's input source, recorded
	// by its inputs sync; a change in that space finds the allocation by it.
	InputOwner string `xorm:"'input_owner'"`
	// InputsSyncedAt is when, in Unix milliseconds, a Managed allocation's
	// inputs were last synced; zero before the first.
	InputsSyncedAt int64 `xorm:"'inputs_synced_at'"`
	// AuthorizationIDs are the Provider Authorizations the Work selected; its
	// repository and service access goes through them, so a credential change
	// finds its Works.
	AuthorizationIDs []byte `xorm:"'authorization_ids'"`
	Submitted        bool   `xorm:"'submitted'"`
	// SandboxID references the allocation's environment once submission
	// resolved it: a Cloud's sandbox identity, or on a Managed Computer the
	// allocation's own identity, standing for the Work's opened directory.
	SandboxID string `xorm:"'sandbox_id'"`
	// AllocationLabel equals the sandbox's allocation label, so an observed
	// sandbox finds its allocation without reading every allocation. MySQL
	// derives it from the identity; it is never written.
	AllocationLabel string `xorm:"<- 'allocation_label'"`
	Version         int    `xorm:"'version'"`
	CreatedAt       int64  `xorm:"'created_at'"`
	UpdatedAt       int64  `xorm:"'updated_at'"`
	DeletedAt       int64  `xorm:"'deleted_at'"`
}

func (*allocationRecord) TableName() string { return "execution_allocation" }

// BeforeInsert records an allocation that uses no authorization as such, one
// whose realization is not named as a Cloud's, as the column defaults, and a
// Managed one with its inputs due.
func (r *allocationRecord) BeforeInsert() {
	if len(r.AuthorizationIDs) == 0 {
		r.AuthorizationIDs = []byte("[]")
	}
	if r.Realization == "" {
		r.Realization = realizationCloud
	}
	if r.Realization == realizationManaged {
		// A Managed Work's machine has no inputs before its first sync.
		r.InputsDue = 1
	}
}

// Provisioning retains the admitted request and exact provider specification
// before any external allocation. Its progress is technical recovery state;
// neither registration nor a Running sandbox establishes Session readiness.
// Recovery tasks drive it one allocation at a time.
type Provisioning struct {
	// ProgressCommitted is wired by composition to the existing Work state stream.
	ProgressCommitted func(context.Context, string)
	db                *xorm.Engine
	sandboxes         *Sandboxes
	credentials       *SandboxCredentials
	// managed is the Managed realization; nil when this deployment has no
	// relay, and then every step of a Managed allocation fails.
	managed *ManagedEnvironment
	now     func() time.Time
	native  NativeLaunchConfig
	initial command.InitialExecutionReader
}

func NewProvisioning(db *xorm.Engine, sandboxes *Sandboxes, credentials *SandboxCredentials, native NativeLaunchConfig, now func() time.Time, initial command.InitialExecutionReader, managed *ManagedEnvironment) *Provisioning {
	return &Provisioning{db: db, sandboxes: sandboxes, credentials: credentials, managed: managed, native: native, now: now, initial: initial}
}

// Register durably accepts the admitted start as the Work's one live
// allocation. Repetition retains the original image, environment and access
// token even after config changes. It performs no external API call and
// therefore cannot start native execution.
func (p *Provisioning) Register(ctx context.Context, request domain.StartExecutionRequest, mounts domain.SandboxMounts) error {
	return p.register(ctx, realizationCloud, request, mounts)
}

// RegisterManaged accepts the admitted start of a Work on a Managed Computer
// as its one live allocation, as Register does for a Cloud. Its environment
// is the Work's directory on the machine, which holds no mounts.
func (p *Provisioning) RegisterManaged(ctx context.Context, request domain.StartExecutionRequest) error {
	return p.register(ctx, realizationManaged, request, domain.SandboxMounts{})
}

func (p *Provisioning) register(ctx context.Context, realization string, request domain.StartExecutionRequest, mounts domain.SandboxMounts) error {
	authority, err := p.authorityOf(realization)
	if err != nil {
		return err
	}
	encoded, err := canonicalRequest(request)
	if err != nil {
		return err
	}
	fingerprint := storageKey(string(encoded))
	existing, found, err := p.live(ctx, request.WorkID)
	if err != nil {
		return err
	}
	if found {
		return sameAllocation(existing, request.WorkID, fingerprint)
	}
	owner := Allocation{WorkID: request.WorkID, ID: uuid.Must(uuid.NewV7()).String()}
	// The initial preparation's thinking level belongs to the Work's first start.
	thinkingLevel := request.ThinkingLevel
	if thinkingLevel == "" && request.ContextMode == domain.ContextFresh {
		initial, found, err := p.initialRequest(ctx, request.WorkID)
		if err != nil {
			return err
		}
		if found {
			thinkingLevel = initial.ThinkingLevel
		}
	}
	launch, err := p.native.prepare(authority.launchLayout(), request, owner, thinkingLevel)
	if err != nil {
		return err
	}
	spec, err := authority.specify(ctx, request, mounts, owner, rand.Text(), rand.Text())
	if err != nil {
		return err
	}
	now := p.now().UnixMilli()
	record := allocationRecord{ID: owner.ID, SessionID: request.WorkID, RequestHash: fingerprint, RequestJSON: encoded, SpecificationJSON: spec.document, Realization: realization, CredentialPlanJSON: []byte("null"), ServiceAccessJSON: []byte("[]"), AuthorizationIDs: spec.authorizations, LaunchCommand: launch.Command, PreparationCommand: launch.PreparationCommand, NativeSessionID: launch.SessionID, Version: 1, CreatedAt: now, UpdatedAt: now}
	_, err = p.db.Context(ctx).Insert(&record)
	if err == nil {
		return nil
	}
	// The unique live allocation per Work rejects a concurrent registration.
	var duplicate *mysql.MySQLError
	if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
		return oops.With("operation", "register_allocation").Wrap(err)
	}
	existing, found, err = p.live(ctx, request.WorkID)
	if err != nil {
		return err
	}
	if !found {
		return errEnvironmentConflict
	}
	return sameAllocation(existing, request.WorkID, fingerprint)
}

// reconcileSubmission advances one live allocation whose environment is not
// yet known. Once a worker has durably claimed submission, all recovery uses
// lookup, including after process restart or an ambiguous response: an
// empty lookup cannot prove an in-flight create failed, so it leaves the
// claimed create unresolved instead of creating a duplicate. Only the authority's
// refusal of the submitted create settles it without an environment: the
// start is rejected.
func (p *Provisioning) reconcileSubmission(ctx context.Context, record allocationRecord) error {
	err := p.reconcile(ctx, record)
	if errors.Is(err, errEnvironmentConflict) {
		return err
	}
	// A refused create recorded as a rejected start is settled, not recovering;
	// its error still reports why.
	recovering := err != nil && !errors.Is(err, errCreationRefused)
	return errors.Join(err, p.recordRecovery(ctx, record, recovering))
}

func (p *Provisioning) reconcile(ctx context.Context, record allocationRecord) error {
	if record.StartRejected {
		return nil
	}
	// Registration precedes preparation. Only an unsubmitted allocation can
	// prove that a rejected prerequisite prevented any external creation.
	if !record.Submitted {
		initial, found, err := p.initialRequest(ctx, record.SessionID)
		if err != nil {
			return err
		}
		if found && !initial.Prepared {
			if initial.Rejected {
				return p.update(ctx, record, "start_rejected = 1")
			}
			return nil
		}
	}
	authority, err := p.authority(record)
	if err != nil {
		return err
	}
	attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	defer release()
	var id string
	if !record.Submitted {
		var plan []byte
		if plan, err = authority.claim(attempt, record); err != nil {
			return err
		}
		// Freeze what the claim fixes and the submission together before
		// creating. A waiting preparation does not suppress durable
		// registration of the start.
		if plan == nil {
			err = p.update(ctx, record, "submitted = 1")
		} else {
			err = p.update(ctx, record, "credential_plan_json = ?, submitted = 1", plan)
		}
		if err != nil {
			return err
		}
		record.Version++
		id, err = authority.create(attempt, record)
		if errors.Is(err, errCreationRefused) {
			return p.settleRefusal(ctx, attempt, authority, record, err)
		}
	} else {
		id, err = authority.find(attempt, record)
	}
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	return p.update(ctx, record, "sandbox_id = ?", id)
}

// settleRefusal settles the one create of an allocation that its authority
// refused. A refusal means nothing was created, so the start is rejected like
// one whose prerequisite was; lookup would never resolve it. An environment
// the lookup still finds was created regardless and is kept instead, so no
// compute is ever left without its allocation. Only a recorded rejection
// returns errCreationRefused; any other outcome stays a recovering failure.
func (p *Provisioning) settleRefusal(ctx, attempt context.Context, authority environmentAuthority, record allocationRecord, refusal error) error {
	id, err := authority.find(attempt, record)
	if err != nil {
		return oops.With("refusal", refusal.Error()).Wrap(err)
	}
	if id != "" {
		return p.update(ctx, record, "sandbox_id = ?", id)
	}
	if err := p.update(ctx, record, "start_rejected = 1"); err != nil {
		return oops.With("refusal", refusal.Error()).Wrap(err)
	}
	return refusal
}

// initialRequest reads the Work's retained initial request, if it has one.
func (p *Provisioning) initialRequest(ctx context.Context, workID string) (command.InitialExecutionRequest, bool, error) {
	request, err := p.initial.Get(ctx, workID)
	if errors.Is(err, command.ErrInitialExecutionNotFound) {
		return command.InitialExecutionRequest{}, false, nil
	}
	return request, err == nil, err
}

// live returns the Work's one live allocation.
func (p *Provisioning) live(ctx context.Context, workID string) (allocationRecord, bool, error) {
	var record allocationRecord
	found, err := p.db.Context(ctx).Where("session_id = ? AND deleted_at = 0", workID).Get(&record)
	if err != nil {
		return record, false, oops.With("operation", "get_live_allocation").Wrap(err)
	}
	return record, found, nil
}

func (p *Provisioning) get(ctx context.Context, id string) (allocationRecord, bool, error) {
	var record allocationRecord
	found, err := p.db.Context(ctx).Where("id = ?", id).Get(&record)
	if err != nil {
		return record, false, oops.With("operation", "get_allocation").Wrap(err)
	}
	return record, found, nil
}

// Assignment is selected only by private call sites above; values remain bound.
func (p *Provisioning) update(ctx context.Context, record allocationRecord, assignment string, values ...any) error {
	args := append(values, p.now().UnixMilli(), record.ID, record.Version)
	result, err := p.db.Context(ctx).Exec(append([]any{"UPDATE execution_allocation SET " + assignment + ", updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND sandbox_id = '' AND deleted_at = 0"}, args...)...)
	if err != nil {
		return oops.With("operation", "update_allocation").Wrap(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if changed != 1 {
		return errEnvironmentConflict
	}
	return nil
}

func sameAllocation(record allocationRecord, workID, fingerprint string) error {
	if record.DeletedAt != 0 || record.SessionID != workID || record.RequestHash != fingerprint {
		return errEnvironmentConflict
	}
	return nil
}

func canonicalRequest(request domain.StartExecutionRequest) ([]byte, error) {
	if err := validateStartRequest(request); err != nil {
		return nil, err
	}
	data, err := json.Marshal(allocationRequestFromStart(request))
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return data, nil
}

func (p *Provisioning) recordRecovery(ctx context.Context, record allocationRecord, recovering bool) error {
	result, err := p.db.Context(ctx).Exec("UPDATE execution_allocation SET preparation_recovering=?,version=version+1 WHERE id=? AND preparation_recovering<>? AND deleted_at=0", recovering, record.ID, recovering)
	if err != nil {
		return oops.Wrap(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if changed > 0 && p.ProgressCommitted != nil {
		p.ProgressCommitted(ctx, record.SessionID)
	}
	return nil
}

// usedAuthorizations lists, as a JSON array, the Provider Authorizations a Work
// selected. Its repository credential grants are issued under them.
func usedAuthorizations(selection domain.WorkAuthorizationSelection) ([]byte, error) {
	ids := []string{}
	for _, authorization := range selection.Authorizations {
		ids = append(ids, authorization.AuthorizationID)
	}
	slices.Sort(ids)
	return json.Marshal(slices.Compact(ids))
}
