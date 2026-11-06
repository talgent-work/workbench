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

type removalRecord struct {
	ID        string `xorm:"pk 'id'"`
	SessionID string `xorm:"'session_id'"`
	SandboxID string `xorm:"'sandbox_id'"`
	Completed bool   `xorm:"'completed'"`
	Version   int    `xorm:"'version'"`
	CreatedAt int64  `xorm:"'created_at'"`
	UpdatedAt int64  `xorm:"'updated_at'"`
	DeletedAt int64  `xorm:"'deleted_at'"`
}

func (*removalRecord) TableName() string { return "execution_removal" }

// Removals retains exact resource identities across process restarts. It is
// technical recovery storage, not capacity admission or a Session state machine.
type Removals struct {
	db  *xorm.Engine
	now func() time.Time
}

func NewRemovals(db *xorm.Engine, now func() time.Time) *Removals { return &Removals{db: db, now: now} }

func (r *Removals) Request(ctx context.Context, owner Allocation, sandboxID string) error {
	if err := owner.validate(); err != nil {
		return err
	}
	if !validSandboxID(sandboxID) {
		return domain.ErrExecutionRequest
	}
	now := r.now().UnixMilli()
	record := removalRecord{ID: owner.ID, SessionID: owner.WorkID, SandboxID: sandboxID, Version: 1, CreatedAt: now, UpdatedAt: now}
	_, err := r.db.Context(ctx).Insert(&record)
	if err == nil {
		return nil
	}
	var duplicate *mysql.MySQLError
	if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
		return oops.Wrap(err)
	}
	var existing removalRecord
	found, err := r.db.Context(ctx).Where("id = ? AND deleted_at = 0", owner.ID).Get(&existing)
	if err != nil {
		return oops.Wrap(err)
	}
	if !found || existing.SessionID != owner.WorkID || existing.SandboxID != sandboxID {
		return errEnvironmentConflict
	}
	return nil // A repeated request never reopens a completed obligation.
}

// retained confirms that the Work's latest allocation already has a removal
// obligation, making a repeated compute release request idempotent.
func (r *Removals) retained(ctx context.Context, workID string) error {
	exists, err := r.db.Context(ctx).Where("session_id = ? AND deleted_at = 0", workID).Exist(&removalRecord{})
	if err != nil {
		return oops.Wrap(err)
	}
	if !exists {
		return errEnvironmentConflict
	}
	return nil
}

// pendingFor returns the allocation's removal obligation while it is unfinished.
func (r *Removals) pendingFor(ctx context.Context, allocationID string) (removalRecord, bool, error) {
	var record removalRecord
	found, err := r.db.Context(ctx).Where("id = ? AND completed = 0 AND deleted_at = 0", allocationID).Get(&record)
	if err != nil {
		return record, false, oops.Wrap(err)
	}
	return record, found, nil
}

// complete records that the allocation's authority released the compute.
func (r *Removals) complete(ctx context.Context, record removalRecord) error {
	result, err := r.db.Context(ctx).Exec("UPDATE execution_removal SET completed = 1, updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND deleted_at = 0", r.now().UnixMilli(), record.ID, record.Version)
	if err != nil {
		return oops.Wrap(err)
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

// reconcileRemoval advances one removal obligation: the allocation's
// authority releases the compute it names, the obligation records the release,
// and only then does Agent learn of it, observing the completed obligation as
// the release of an authority that cannot observe its own. An error, or
// compute not released yet, leaves the obligation pending.
func (r *Reclaimer) reconcileRemoval(ctx context.Context, removals *Removals, removal removalRecord, allocation allocationRecord, authority environmentAuthority) error {
	attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	released, err := authority.release(attempt, allocation, removal)
	release()
	if err != nil || !released {
		return err
	}
	// Only completion changes the obligation; an unchanged one is not rewritten.
	if err := removals.complete(ctx, removal); err != nil {
		return err
	}
	return r.confirm(ctx, removal.SessionID)
}
