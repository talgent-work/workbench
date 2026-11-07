package domain

import (
	"errors"
	"time"
)

// ErrInvalidLeaseTerms means lease terms whose renewal threshold is not below
// their duration, which would renew a lease on every reconciliation.
var ErrInvalidLeaseTerms = errors.New("agent: invalid lease terms")

// LeaseTerms are how a Cloud environment is leased: each lease runs for
// Duration, and a wanted one is renewed once less than RenewalThreshold
// remains, well before the environment authority deletes it. Zero terms lease
// nothing, as on a Managed Computer.
type LeaseTerms struct {
	Duration         time.Duration
	RenewalThreshold time.Duration
}

// Validate admits terms that lease environments: a positive renewal threshold
// below a lease's duration.
func (t LeaseTerms) Validate() error {
	if t.RenewalThreshold <= 0 || t.RenewalThreshold >= t.Duration {
		return ErrInvalidLeaseTerms
	}
	return nil
}

// leases reports whether the terms lease environments at all.
func (t LeaseTerms) leases() bool { return t.Validate() == nil }

// RecordLease keeps the later of the returned and the recorded expiry of its
// bound environment, so a stale observation or a shorter answer never
// shortens its lease (Environment lease). Without a bound environment there
// is no lease to record.
func (e *AgentExecution) RecordLease(expiresAt time.Time) bool {
	if e.Environment == "" || expiresAt.IsZero() || !expiresAt.After(e.Lease) {
		return false
	}
	e.Lease = expiresAt.UTC()
	return true
}

// leaseSteps renew the bound environment's lease, as observed now, once less
// than the renewal threshold remains, to the later of its recorded expiry and
// the lease duration from now, so a renewal only ever extends it
// (Environment lease). An environment created without an expiry has no lease
// and is never renewed.
func (e *AgentExecution) leaseSteps(bound ObservedEnvironment, terms LeaseTerms, now time.Time) []ExecutionStep {
	e.RecordLease(bound.LeaseExpiresAt)
	if e.Lease.IsZero() || !terms.leases() || e.Lease.Sub(now) >= terms.RenewalThreshold {
		return nil
	}
	renewed := now.Add(terms.Duration).UTC()
	if e.Lease.After(renewed) {
		renewed = e.Lease
	}
	return []ExecutionStep{{Kind: StepRenewLease, LeaseExpiresAt: renewed}}
}
