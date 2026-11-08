package execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"slices"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// Each external attempt leaves time for durable bookkeeping and other records.
const reconciliationAttemptTimeout = 5 * time.Second

// Provisioning holds what realizing an execution's environment needs: its
// records, both realizations' environment authorities and the launch
// configuration. It fixes each execution's environment specification once,
// before its creation is submitted; it decides no step.
type Provisioning struct {
	// ProgressCommitted is wired by composition to the existing Work state stream.
	ProgressCommitted func(context.Context, string)
	records
	sandboxes   *Sandboxes
	credentials *SandboxCredentials
	// managed is the Managed realization; nil when this deployment has no
	// relay, and then every step of a Managed execution fails.
	managed *ManagedEnvironment
	now     func() time.Time
	native  NativeLaunchConfig
	// attemptTimeout bounds one short attempt against the environment
	// authority, such as an observation; it is reconciliationAttemptTimeout
	// except in tests that exercise its expiry. A creation waits for its
	// authority's answer instead.
	attemptTimeout time.Duration
}

func NewProvisioning(db *xorm.Engine, sandboxes *Sandboxes, credentials *SandboxCredentials, native NativeLaunchConfig, now func() time.Time, managed *ManagedEnvironment) *Provisioning {
	return &Provisioning{records: records{db: db}, sandboxes: sandboxes, credentials: credentials, managed: managed, native: native, now: now, attemptTimeout: reconciliationAttemptTimeout}
}

// specify fixes the execution's environment specification, launch commands
// and the credential plan its submission freezes, from its LaunchSpecification
// and what Application supplied: the Mount Grants and repository access of a
// Cloud start. A specified execution keeps the image, environment and access
// tokens it was given.
func (p *Provisioning) specify(ctx context.Context, record executionRecord, supply command.CreationSupply) error {
	if record.specified() {
		return nil
	}
	authority, err := p.authority(record)
	if err != nil {
		return err
	}
	launch, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return oops.With("operation", "decode_execution_launch").Wrap(err)
	}
	owner := SandboxOwner{WorkID: record.SessionID, ID: record.ID}
	native, err := p.native.prepare(authority.launchLayout(), launch, owner, launch.ThinkingLevel)
	if err != nil {
		return err
	}
	spec, err := authority.specify(ctx, launch, supply.Mounts, owner, rand.Text(), rand.Text())
	if err != nil {
		return err
	}
	record.SpecificationJSON = spec.document
	plan, err := authority.claim(record, supply)
	if err != nil {
		return err
	}
	if plan == nil {
		plan = []byte("null")
	}
	_, err = p.db.Context(ctx).Exec(`UPDATE agent_execution SET specification_json = ?, authorization_ids = ?, launch_command = ?, preparation_command = ?, native_session_id = ?, credential_plan_json = ?, updated_at = ?
 WHERE id = ? AND JSON_TYPE(specification_json) = 'NULL'`, spec.document, spec.authorizations, native.Command, native.PreparationCommand, native.SessionID, plan, p.now().UnixMilli(), record.ID)
	if err != nil {
		return oops.With("operation", "specify_execution").Wrap(err)
	}
	return nil
}

// recordRecovery records whether the latest attempt at a step left the start
// recovering, which its startup progress shows.
func (p *Provisioning) recordRecovery(ctx context.Context, record executionRecord, recovering bool) error {
	result, err := p.db.Context(ctx).Exec("UPDATE agent_execution SET preparation_recovering = ?, updated_at = ? WHERE id = ? AND preparation_recovering <> ? AND deleted_at = 0", recovering, p.now().UnixMilli(), record.ID, recovering)
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

// technical writes adapter-owned columns of a live execution with the
// recorded environment; the execution's facts and version are untouched.
func (p *Provisioning) technical(ctx context.Context, record executionRecord, assignment string, values ...any) error {
	args := append(values, p.now().UnixMilli(), record.ID, record.SandboxID)
	if _, err := p.db.Context(ctx).Exec(append([]any{"UPDATE agent_execution SET " + assignment + ", updated_at = ? WHERE id = ? AND sandbox_id = ? AND deleted_at = 0"}, args...)...); err != nil {
		return oops.With("operation", "record_execution_progress").Wrap(err)
	}
	if p.ProgressCommitted != nil {
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
