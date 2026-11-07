package execution

import (
	"context"
	"errors"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// StreamReading is the process, and its endpoint, whose stream Dispatcher has
// recorded for a Work.
type StreamReading struct {
	ProcessID, Endpoint string
}

// StreamOwners reads which Harness process Dispatcher reads for a Work, so the
// sweep finds a connected allocation whose stream nobody reads without asking
// the environment authority.
type StreamOwners interface {
	Reading(ctx context.Context, workID string) (StreamReading, bool, error)
}

// EnvironmentRecovery advances one allocation at a time through onboarding:
// submission, first delivery, preparation with the Harness start, and later
// reconnects. Each step reports the step the allocation needs next. A sandbox
// observation, a Work commit, a connection end or the periodic sweep names the
// allocation, and completed history is read only when one of its late
// sandboxes is observed.
type EnvironmentRecovery struct {
	provisioning *Provisioning
	connections  *NativeConnections
	reclaimer    *Reclaimer
	removals     *Removals
	owners       StreamOwners
}

func NewEnvironmentRecovery(provisioning *Provisioning, connections *NativeConnections, reclaimer *Reclaimer, removals *Removals, owners StreamOwners) (*EnvironmentRecovery, error) {
	if provisioning == nil || connections == nil || reclaimer == nil || removals == nil || owners == nil {
		return nil, errors.New("agent: provisioning, connections, removal and stream owners are required for environment recovery")
	}
	return &EnvironmentRecovery{provisioning: provisioning, connections: connections, reclaimer: reclaimer, removals: removals, owners: owners}, nil
}

// ReconcileAllocation advances one allocation from its durable facts: a live
// allocation resolves its submission, receives its first delivery once its
// environment runs, or has its credentials drained once its stop is admitted; a
// retired one has its late or duplicate environments removed; an unfinished
// removal obligation is advanced. A connected allocation needs no call to the
// environment authority. Each failure is retried by the caller without
// blocking other allocations.
func (r *EnvironmentRecovery) ReconcileAllocation(ctx context.Context, allocationID string) (command.AllocationStep, error) {
	p := r.provisioning
	record, found, err := p.get(ctx, allocationID)
	if err != nil || !found {
		return command.AllocationSettled, err
	}
	authority, err := p.authority(record)
	if err != nil {
		return command.AllocationSettled, withAllocation(err, record)
	}
	next := command.AllocationSettled
	var failures []error
	switch {
	case record.DeletedAt == 0 && !record.StartRejected:
		if record.SandboxID == "" {
			if err := p.reconcileSubmission(ctx, record); err != nil {
				return next, withAllocation(err, record)
			}
			if record, found, err = p.get(ctx, allocationID); err != nil || !found {
				return next, err
			}
		}
		if record.DeletedAt == 0 && record.SandboxID != "" {
			next, err = r.advance(ctx, record)
			failures = append(failures, err)
		}
	case record.DeletedAt != 0 && record.Submitted:
		// Only an authority whose creation can leave surplus environments
		// has late ones to remove.
		if surplus, ok := authority.(surplusEnvironments); ok {
			failures = append(failures, surplus.pruneRetired(ctx, record))
		}
	}
	removal, pending, err := r.removals.pendingFor(ctx, allocationID)
	if err != nil {
		return next, withAllocation(errors.Join(append(failures, err)...), record)
	}
	if pending {
		failures = append(failures, r.reclaimer.reconcileRemoval(ctx, r.removals, removal, record, authority))
	}
	return next, withAllocation(errors.Join(failures...), record)
}

// advance takes a live allocation with an environment one step from its facts.
func (r *EnvironmentRecovery) advance(ctx context.Context, record allocationRecord) (command.AllocationStep, error) {
	n := r.connections
	draining, held, err := r.holding(ctx, record)
	if err != nil {
		return command.AllocationSettled, err
	}
	if !held {
		// A run its machine fenced retires here when the Work's own request
		// to retire it was lost.
		_, err := retireOutlived(ctx, r.provisioning, r.removals, record)
		return command.AllocationSettled, err
	}
	switch {
	case draining:
		return command.AllocationSettled, n.drain(ctx, record)
	case !record.SandboxReady:
		attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
		ready, err := n.deliver(attempt, record)
		release()
		if err := r.settle(ctx, record, err); err != nil || !ready {
			return command.AllocationSettled, err
		}
		return command.AllocationPrepare, nil
	}
	return r.nextStep(ctx, record, false, "")
}

// nextStep judges, from durable facts and Dispatcher's stream ownership, the
// step a live allocation with an environment needs. It is the one place every path
// (reconciliation, sandbox observation and the sweep) decides it. An endpoint,
// when known, is compared with the one Dispatcher reads.
func (r *EnvironmentRecovery) nextStep(ctx context.Context, record allocationRecord, draining bool, endpoint string) (command.AllocationStep, error) {
	switch {
	case draining, !record.SandboxReady:
		return command.AllocationReconcile, nil
	case record.ProcessExited:
		return command.AllocationSettled, nil
	case record.ProcessID == "" || !record.EnvironmentPrepared:
		return command.AllocationPrepare, nil
	}
	reading, found, err := r.owners.Reading(ctx, record.SessionID)
	if err != nil {
		return command.AllocationSettled, withAllocation(err, record)
	}
	if !found || reading.ProcessID != record.ProcessID || (endpoint != "" && reading.Endpoint != endpoint) {
		return command.AllocationReconnect, nil
	}
	authority, err := r.provisioning.authority(record)
	if err != nil {
		return command.AllocationSettled, withAllocation(err, record)
	}
	delivery, delivers := authority.(credentialDelivery)
	if !delivers {
		return command.AllocationSettled, nil
	}
	if record.CredentialsExpireAt > 0 && record.CredentialsExpireAt <= r.provisioning.now().UnixMilli() {
		// A delivery whose declared expiry passed, or that a change marked
		// due, is fetched once more; its next expiry, or none, is recorded.
		return command.AllocationDeliver, nil
	}
	digest, err := delivery.modelDigest(record)
	if err != nil {
		return command.AllocationSettled, withAllocation(err, record)
	}
	if digest != "" && digest != record.ModelCredentialDigest {
		// A rotated Model Configuration key reaches running Works.
		return command.AllocationDeliver, nil
	}
	if inputs, syncs := authority.(inputDelivery); syncs && inputs.inputsDue(record) {
		// Inputs a change or a failed attempt left due, or synced longer ago
		// than the refresh interval, are synced again.
		return command.AllocationSyncInputs, nil
	}
	return command.AllocationSettled, nil
}

// holding reports whether the allocation still backs its Work's Harness
// process and, if so, whether the Work's stop is admitted.
func (r *EnvironmentRecovery) holding(ctx context.Context, record allocationRecord) (draining, held bool, err error) {
	draining, err = r.connections.credentials.works.draining(ctx, record)
	if errors.Is(err, errAllocationNotHeld) {
		// The process exited or never started; removal follows the Work.
		return false, false, nil
	}
	return draining, err == nil, err
}

// settle records whether an onboarding attempt left the start recovering. A
// conflict means another step changed the allocation and says nothing about
// recovery.
func (r *EnvironmentRecovery) settle(ctx context.Context, record allocationRecord, err error) error {
	if errors.Is(err, errEnvironmentConflict) {
		return withAllocation(err, record)
	}
	return withAllocation(errors.Join(err, r.provisioning.recordRecovery(ctx, record, err != nil)), record)
}

// PrepareAllocation waits for the allocation's preparation to exit, no longer
// than the Work's Startup Deadline, then starts the Harness and connects it at
// once. A Work past its deadline is failed by its own recovery, which drains
// the allocation.
func (r *EnvironmentRecovery) PrepareAllocation(ctx context.Context, allocationID string) error {
	record, found, err := r.provisioning.get(ctx, allocationID)
	if err != nil || !found || record.DeletedAt != 0 || record.StartRejected || !record.SandboxReady || record.ProcessExited {
		return err
	}
	draining, held, err := r.holding(ctx, record)
	if err != nil || !held || draining {
		return withAllocation(err, record)
	}
	work, err := r.connections.credentials.works.sessions.Get(ctx, record.SessionID)
	if err != nil {
		return withAllocation(err, record)
	}
	deadline := work.HarnessProcess.StartupDeadlineAt()
	if !r.provisioning.now().Before(deadline) {
		return nil
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	err = r.connections.prepare(bounded, record)
	if errors.Is(err, errAllocationNotHeld) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
		// The Work's stop was admitted, or its Startup Deadline passed, while
		// it prepared; the Work's recovery settles it and drains the allocation.
		err = nil
	}
	return r.settle(ctx, record, err)
}

// DeliverCredentials replaces a delivered allocation's credentials with those
// Marketplace supplies now. A Work whose stop is admitted is drained instead.
func (r *EnvironmentRecovery) DeliverCredentials(ctx context.Context, allocationID string) error {
	record, found, err := r.provisioning.get(ctx, allocationID)
	if err != nil || !found || record.DeletedAt != 0 || !record.SandboxReady {
		return err
	}
	draining, held, err := r.holding(ctx, record)
	if err != nil || !held || draining {
		return withAllocation(err, record)
	}
	attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	defer release()
	err = r.connections.redeliver(attempt, record)
	if errors.Is(err, errAllocationNotHeld) {
		return nil
	}
	return withAllocation(err, record)
}

// MarkCredentialsChanged records, on every live allocation whose Work selected
// the authorization or reaches repositories through it, that its delivery is
// due, and returns the delivered ones. The mark is durable and changes the
// allocation's version, so a delivery that read the earlier credential fails
// its write and runs again, and a lost wake is found by the sweep.
func (r *EnvironmentRecovery) MarkCredentialsChanged(ctx context.Context, authorizationID string) ([]string, error) {
	db := r.provisioning.db
	_, err := db.Context(ctx).Exec(`UPDATE execution_allocation SET credentials_expire_at = ?, version = version + 1
 WHERE ? MEMBER OF(authorization_ids) AND deleted_at = 0`, staleDelivery.UnixMilli(), authorizationID)
	if err != nil {
		return nil, oops.With("operation", "mark_credentials_changed").Wrap(err)
	}
	var ids []string
	err = db.Context(ctx).SQL(`SELECT id FROM execution_allocation
 WHERE ? MEMBER OF(authorization_ids) AND deleted_at = 0 AND sandbox_ready = 1`, authorizationID).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "list_allocations_using_authorization").Wrap(err)
	}
	return ids, nil
}

// ReconnectAllocation hands the allocation's running process to Dispatcher
// again. An absent environment is the loss of the Work's environment. A start
// whose first Connect failed is reported recovering until one succeeds.
func (r *EnvironmentRecovery) ReconnectAllocation(ctx context.Context, allocationID string) error {
	record, found, err := r.provisioning.get(ctx, allocationID)
	if err != nil || !found || record.DeletedAt != 0 || record.ProcessID == "" || record.ProcessExited {
		return err
	}
	attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	err = r.connections.reconnect(attempt, record)
	release()
	return r.settle(ctx, record, err)
}

// ObserveSandbox checks an allocation after the environment authority reported
// a change to one of its sandboxes: the loss of the primary, its endpoint
// moving away from the one Dispatcher reads, or a surplus sandbox. Anything
// else continues from the allocation's facts.
func (r *EnvironmentRecovery) ObserveSandbox(ctx context.Context, allocationID string) (command.AllocationStep, error) {
	p := r.provisioning
	record, found, err := p.get(ctx, allocationID)
	if err != nil || !found {
		return command.AllocationSettled, err
	}
	if record.DeletedAt != 0 || record.StartRejected || record.SandboxID == "" || !record.SandboxReady {
		return command.AllocationReconcile, nil
	}
	attempt, release := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	defer release()
	box, state, err := r.connections.resolve(attempt, record)
	if err != nil || state == environmentLost {
		return command.AllocationSettled, withAllocation(err, record)
	}
	authority, err := p.authority(record)
	if err != nil {
		return command.AllocationSettled, withAllocation(err, record)
	}
	if surplus, ok := authority.(surplusEnvironments); ok {
		if err := surplus.prune(attempt, record, box); err != nil {
			return command.AllocationSettled, withAllocation(err, record)
		}
	}
	if state == environmentPending {
		return command.AllocationSettled, nil
	}
	draining, held, err := r.holding(ctx, record)
	if err != nil || !held {
		return command.AllocationSettled, err
	}
	return r.nextStep(ctx, record, draining, box.endpoint)
}

func withAllocation(err error, record allocationRecord) error {
	if err == nil {
		return nil
	}
	return oops.With("allocation_id", record.ID, "agent_work_id", record.SessionID).Wrap(err)
}

// LiveAllocation returns the Work's one live allocation.
func (r *EnvironmentRecovery) LiveAllocation(ctx context.Context, workID string) (string, bool, error) {
	record, found, err := r.provisioning.live(ctx, workID)
	return record.ID, found, err
}

// ListOutstandingAllocations pages live allocations and those whose removal is
// unfinished, each with the step it is missing. It reads only durable facts
// and Dispatcher's stream ownership: a connected allocation whose stream is
// read needs nothing. A retired allocation with a completed removal is
// history and is never listed.
func (r *EnvironmentRecovery) ListOutstandingAllocations(ctx context.Context, after string, limit int) ([]command.AllocationStatus, error) {
	var ids []string
	err := r.provisioning.db.Context(ctx).SQL(`SELECT id FROM (
 SELECT id FROM execution_allocation WHERE deleted_at = 0 AND start_rejected = 0
 UNION
 SELECT id FROM execution_removal WHERE completed = 0 AND deleted_at = 0
) AS outstanding WHERE id > ? ORDER BY id LIMIT ?`, after, limit).Find(&ids)
	if err != nil {
		return nil, oops.With("operation", "list_outstanding_allocations").Wrap(err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var records []allocationRecord
	if err := r.provisioning.db.Context(ctx).In("id", ids).Find(&records); err != nil {
		return nil, oops.With("operation", "read_outstanding_allocations").Wrap(err)
	}
	byID := make(map[string]allocationRecord, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	// An allocation whose facts cannot be read now is left to the next sweep;
	// the others on the page are still judged.
	statuses := make([]command.AllocationStatus, 0, len(ids))
	var failures []error
	for _, id := range ids {
		step, err := r.missingStep(ctx, byID[id])
		failures = append(failures, err)
		statuses = append(statuses, command.AllocationStatus{AllocationID: id, Step: step})
	}
	return statuses, errors.Join(failures...)
}

// missingStep judges an outstanding allocation from its facts. A record that
// is not live has only its removal outstanding.
func (r *EnvironmentRecovery) missingStep(ctx context.Context, record allocationRecord) (command.AllocationStep, error) {
	if record.ID == "" || record.DeletedAt != 0 || record.SandboxID == "" {
		return command.AllocationReconcile, nil
	}
	draining, held, err := r.holding(ctx, record)
	if err != nil || !held {
		return command.AllocationSettled, err
	}
	return r.nextStep(ctx, record, draining, "")
}

// AllocationForSandbox returns the allocation whose label an observed sandbox
// carries, retired allocations included.
func (r *EnvironmentRecovery) AllocationForSandbox(ctx context.Context, label string) (string, bool, error) {
	if label == "" {
		return "", false, nil
	}
	var ids []string
	if err := r.provisioning.db.Context(ctx).Table("execution_allocation").Cols("id").Where("allocation_label = ?", label).Limit(2).Find(&ids); err != nil {
		return "", false, oops.With("operation", "find_sandbox_allocation").Wrap(err)
	}
	if len(ids) != 1 {
		// Absence, or a label that does not identify exactly one allocation,
		// cannot prove ownership.
		return "", false, nil
	}
	return ids[0], true, nil
}
