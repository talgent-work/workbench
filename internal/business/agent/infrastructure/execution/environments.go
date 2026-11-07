package execution

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type Environments struct {
	// AllocationChanged is wired by composition to wake the allocation's
	// reconciliation after it is registered or retired.
	AllocationChanged func(context.Context, string)
	processes         *ProcessObservations
	provisioning      *Provisioning
}

func NewEnvironments(provisioning *Provisioning, processes *ProcessObservations) (*Environments, error) {
	if provisioning == nil || processes == nil {
		return nil, errors.New("agent: provisioning and process observations are required")
	}
	return &Environments{provisioning: provisioning, processes: processes}, nil
}

func (e *Environments) Create(ctx context.Context, request domain.StartExecutionRequest, mounts domain.SandboxMounts) error {
	return e.create(ctx, request, func() error { return e.provisioning.Register(ctx, request, mounts) })
}

func (e *Environments) CreateManaged(ctx context.Context, request domain.StartExecutionRequest) error {
	return e.create(ctx, request, func() error { return e.provisioning.RegisterManaged(ctx, request) })
}

func (e *Environments) create(ctx context.Context, request domain.StartExecutionRequest, register func() error) error {
	if err := validateStartRequest(request); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := register(); err != nil {
		return err
	}
	// The wake is best effort: registration succeeded, and the periodic sweep
	// wakes every live allocation if this lookup fails.
	if record, found, err := e.provisioning.live(ctx, request.WorkID); err == nil && found {
		e.changed(ctx, record.ID)
	}
	return nil
}

func (e *Environments) changed(ctx context.Context, allocationID string) {
	if e.AllocationChanged != nil {
		e.AllocationChanged(ctx, allocationID)
	}
}

func (e *Environments) StartRejected(ctx context.Context, workID string) (bool, error) {
	record, found, err := e.provisioning.live(ctx, workID)
	if err != nil {
		return false, err
	}
	return found && record.StartRejected && record.SandboxID == "", nil
}

// StartAbandoned reports whether the Work's live allocation never launched a
// Harness process and, past its Startup Deadline, no longer can.
func (e *Environments) StartAbandoned(ctx context.Context, workID string) (bool, error) {
	record, found, err := e.provisioning.live(ctx, workID)
	if err != nil {
		return false, err
	}
	return found && record.SandboxID != "" && abandoned(record, e.provisioning.now()), nil
}

// Delete retires the Work's live allocation after its process exited, its
// start was rejected or abandoned, its environment was lost, or when its
// Work reclaims the run beyond reach; whether a sandbox identity was ever
// recorded does not matter, since release finds the allocation's compute by
// its ownership. A repeated request after retirement changes nothing.
func (e *Environments) Delete(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrExecutionRequest
	}
	record, found, err := e.provisioning.live(ctx, workID)
	if err != nil {
		return err
	}
	if !found {
		var retired allocationRecord
		found, err := e.provisioning.db.Context(ctx).Where("session_id = ?", workID).Desc("created_at", "id").Get(&retired)
		if err != nil {
			return oops.Wrap(err)
		}
		if found && retired.DeletedAt != 0 {
			return nil
		}
		return errEnvironmentConflict
	}
	if record.StartRejected && record.SandboxID == "" {
		// No compute exists: rejection either won the same CAS that guards
		// submission, or the provider refused the one submitted create.
		// Retiring it releases it at once, with nothing to delete.
		now := e.provisioning.now().UnixMilli()
		return e.provisioning.update(ctx, record, "deleted_at = ?, released_at = ?", now, now)
	}
	// Save-to-send can span cleanup and a new start. Recheck the specific
	// resource selected above: only its acknowledged exit, its lost
	// environment, its abandonment or its current run's reclamation beyond
	// reach authorizes deletion, never a stale Work-level request.
	if !record.ProcessExited && !abandoned(record, e.provisioning.now()) {
		unreached, err := e.provisioning.unreached(ctx, record)
		if err != nil {
			return err
		}
		if !unreached {
			lost, err := e.processes.environmentLost(ctx, record)
			if err != nil || !lost {
				return err
			}
		}
	}
	if err := retire(ctx, e.provisioning, record); err != nil {
		return err
	}
	e.changed(ctx, record.ID)
	return nil
}

// RetireFenced retires the Work's live allocation once its Managed Computer
// fenced the run, as the Work requested: no compute is reclaimed, and the
// release deletes the run's process handles once the machine is back. A
// request that finds no live allocation, or a newer start's, changes nothing.
func (e *Environments) RetireFenced(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrExecutionRequest
	}
	record, found, err := e.provisioning.live(ctx, workID)
	if err != nil || !found {
		return err
	}
	retired, err := retireOutlived(ctx, e.provisioning, record)
	if err != nil || !retired {
		return err
	}
	e.changed(ctx, record.ID)
	return nil
}

// retireOutlived retires a live allocation on a fencing authority whose Work
// no longer holds the run it backs. It is the one place such an allocation
// retires: on the Work's request to retire its fenced run, and in recovery
// when that request was lost.
func retireOutlived(ctx context.Context, provisioning *Provisioning, record allocationRecord) (bool, error) {
	outlived, err := provisioning.outlived(ctx, record)
	if err != nil || !outlived {
		return false, err
	}
	return true, retire(ctx, provisioning, record)
}

// retire ends the allocation's wanting of its environment, so no further
// connection is attempted, and leaves its release outstanding until its
// recovery confirms it. Repeating it is idempotent; no HOME or workspace
// content is touched.
func retire(ctx context.Context, provisioning *Provisioning, record allocationRecord) error {
	now := provisioning.now().UnixMilli()
	_, err := provisioning.db.Context(ctx).Exec("UPDATE execution_allocation SET deleted_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND session_id = ? AND deleted_at = 0", now, now, record.ID, record.SessionID)
	if err != nil {
		return oops.With("operation", "retire_execution_allocation").Wrap(err)
	}
	return nil
}

// abandonmentMargin keeps a launch begun just before the Startup Deadline
// from being taken for abandonment while its process binding is recorded.
const abandonmentMargin = time.Minute

// abandoned reports an allocation whose start never launched a Harness
// process and no longer can: no process is bound, and launch is refused past
// the Startup Deadline, counted here from the allocation, which is never
// earlier than the start it serves. A newer start's allocation is younger
// than its own deadline, so a stale release never reaches it.
func abandoned(record allocationRecord, now time.Time) bool {
	return record.ProcessID == "" && record.CreatedAt > 0 &&
		!now.Before(time.UnixMilli(record.CreatedAt).Add(domain.StartupDeadline+abandonmentMargin))
}
