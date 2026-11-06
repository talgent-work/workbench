//go:build integration

package execution

import (
	"context"
	"errors"
)

// Recovery tasks reconcile one allocation at a time. These helpers drive one
// pass of a single step over every allocation it applies to, as the periodic
// sweep followed by each allocation's task would.

func eachAllocation(ctx context.Context, p *Provisioning, where string, step func(context.Context, allocationRecord) error) error {
	var records []allocationRecord
	if err := p.db.Context(ctx).Where(where).Asc("id").Find(&records); err != nil {
		return err
	}
	var failures []error
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		failures = append(failures, step(ctx, record))
	}
	return errors.Join(failures...)
}

// submitPending resolves the submission of every live allocation whose sandbox
// is not yet known.
func submitPending(ctx context.Context, p *Provisioning) error {
	return eachAllocation(ctx, p, "sandbox_id = '' AND start_rejected = 0 AND deleted_at = 0", func(ctx context.Context, record allocationRecord) error {
		attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
		defer release()
		return p.reconcileSubmission(attempt, record)
	})
}

// connectLive takes every live allocation with a known sandbox through the
// onboarding step it needs, as its tasks would: drain a stopping Work, first
// delivery, preparation with the Harness start, or a reconnect.
func connectLive(ctx context.Context, n *NativeConnections) error {
	return eachAllocation(ctx, n.provisioning, "sandbox_id <> '' AND deleted_at = 0", func(ctx context.Context, record allocationRecord) error {
		return onboard(ctx, n, record)
	})
}

func onboard(ctx context.Context, n *NativeConnections, record allocationRecord) error {
	draining, err := n.credentials.works.draining(ctx, record)
	if err != nil {
		return err
	}
	if draining {
		return n.drain(ctx, record)
	}
	if !record.SandboxReady {
		ready, err := n.deliver(ctx, record)
		if err := errors.Join(err, n.provisioning.recordRecovery(ctx, record, err != nil)); err != nil || !ready {
			return err
		}
		current, _, err := n.provisioning.get(ctx, record.ID)
		if err != nil {
			return err
		}
		record = current
	}
	if record.ProcessID == "" || !record.EnvironmentPrepared {
		err := n.prepare(ctx, record)
		return errors.Join(err, n.provisioning.recordRecovery(ctx, record, err != nil))
	}
	err = n.reconnect(ctx, record)
	return errors.Join(err, n.provisioning.recordRecovery(ctx, record, err != nil))
}

// unread is a Dispatcher that reads no Work's stream.
type unread struct{}

func (unread) Reading(context.Context, string) (StreamReading, bool, error) {
	return StreamReading{}, false, nil
}

// pruneRetiredAll removes late sandboxes of every retired, submitted allocation.
func pruneRetiredAll(ctx context.Context, p *Provisioning) error {
	return eachAllocation(ctx, p, "retired = 1 AND submitted = 1", func(ctx context.Context, record allocationRecord) error {
		attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
		defer release()
		authority, err := p.authority(record)
		if err != nil {
			return err
		}
		if surplus, ok := authority.(surplusEnvironments); ok {
			return surplus.pruneRetired(attempt, record)
		}
		return nil
	})
}

// reclaimPending advances every unfinished removal obligation of a Cloud
// sandbox, whose release needs only the removal.
func reclaimPending(ctx context.Context, r *Reclaimer, removals *Removals, cloud cloudEnvironment) error {
	var records []removalRecord
	if err := removals.db.Context(ctx).Where("completed = 0 AND deleted_at = 0").Asc("id").Find(&records); err != nil {
		return err
	}
	var failures []error
	for _, record := range records {
		failures = append(failures, r.reconcileRemoval(ctx, removals, record, allocationRecord{}, cloud))
	}
	return errors.Join(failures...)
}

// pendingRemovals lists unfinished removal obligations.
func pendingRemovals(ctx context.Context, removals *Removals) ([]removalRecord, error) {
	var records []removalRecord
	err := removals.db.Context(ctx).Where("completed = 0 AND deleted_at = 0").Asc("id").Find(&records)
	return records, err
}
