package domain

import (
	"fmt"
	"time"
)

// InputSyncAttempts bounds the failed attempts at syncing a Managed
// Computer's input copies since the change that made them due (Input copy
// retry). One wake of the execution's reconciliation attempts the sync at
// most RecoveryRetries+1 times and the recovery sweep wakes it again each
// interval, so the ceiling spans about three wakes. The attempt that reaches
// it abandons the sync, and only a later input change starts a new attempt.
const InputSyncAttempts = 30

// InputSync is, on a Managed Computer, the input changes its machine has not
// received yet, when its copies were last synced, the failed attempts at
// syncing them since then or since the latest change, and, once those reached
// InputSyncAttempts, why the sync was abandoned.
type InputSync struct {
	Due       int64
	SyncedAt  time.Time
	Failures  int
	Abandoned string
}

// MarkInputsChanged records an input change its Managed Computer has not
// received, so its copies are synced again (Input copy sync). The change
// starts a new attempt, even after an abandoned one.
func (e *AgentExecution) MarkInputsChanged() bool {
	if e.Retired() || e.Realization != RealizationManaged {
		return false
	}
	e.Inputs.Due++
	e.Inputs.Failures, e.Inputs.Abandoned = 0, ""
	return true
}

// RecordInputSync records that its Managed Computer received the input
// changes due when the sync began, at syncedAt.
func (e *AgentExecution) RecordInputSync(synced int64, syncedAt time.Time) bool {
	if e.Realization != RealizationManaged || syncedAt.IsZero() {
		return false
	}
	e.Inputs.Due = max(e.Inputs.Due-synced, 0)
	e.Inputs.SyncedAt = syncedAt.UTC()
	e.Inputs.Failures, e.Inputs.Abandoned = 0, ""
	return true
}

// RecordInputSyncFailure records a failed attempt at syncing its input
// copies, named by cause (Input copy retry). The attempt that reaches
// InputSyncAttempts abandons the sync, naming the cause, so it is no longer
// decided until a later change. It reports whether it changed and whether
// this attempt abandoned the sync.
func (e *AgentExecution) RecordInputSyncFailure(cause string) (changed, abandoned bool) {
	if e.Realization != RealizationManaged || e.Retired() || e.Inputs.Abandoned != "" {
		return false, false
	}
	e.Inputs.Failures++
	if e.Inputs.Failures < InputSyncAttempts {
		return true, false
	}
	e.Inputs.Abandoned = fmt.Sprintf("Input copies not synced after %d attempts. Last error: %s.", e.Inputs.Failures, startCause(cause))
	return true, true
}

// inputsDue reports a Managed Computer's input copies a change left due, or
// synced longer ago than their refresh interval (Managed input copies),
// unless their sync was abandoned.
func (e *AgentExecution) inputsDue(observation EnvironmentObservation, now time.Time) bool {
	if e.Realization != RealizationManaged || e.Inputs.Abandoned != "" {
		return false
	}
	if e.Inputs.Due > 0 || e.Inputs.SyncedAt.IsZero() {
		return true
	}
	return observation.InputsRefresh > 0 && !now.Before(e.Inputs.SyncedAt.Add(observation.InputsRefresh))
}
