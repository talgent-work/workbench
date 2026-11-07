package domain

import "slices"

// Safe reasons of a repository that was not checked out. Raw Git
// diagnostics never leave the execution environment.
const (
	CheckoutUnavailable   = "checkout_unavailable"
	CheckoutTimedOut      = "timed_out"
	CheckoutIncomplete    = "preparation_incomplete"
	CheckoutBranchMissing = "branch_unavailable"
	CheckoutTargetTaken   = "target_conflict"
)

// IsCheckoutReason reports whether reason is one of the safe reasons above.
func IsCheckoutReason(reason string) bool {
	switch reason {
	case CheckoutUnavailable, CheckoutTimedOut, CheckoutIncomplete, CheckoutBranchMissing, CheckoutTargetTaken:
		return true
	}
	return false
}

// RepositoryCheckout is one selected repository's checkout outcome: checked
// out, or not checked out with its reason.
type RepositoryCheckout struct {
	RepositoryID string
	CheckedOut   bool
	// Reason is the safe reason it was not checked out; empty when it was.
	Reason string
}

func (c RepositoryCheckout) valid() bool {
	if c.RepositoryID == "" {
		return false
	}
	if c.CheckedOut {
		return c.Reason == ""
	}
	return IsCheckoutReason(c.Reason)
}

// RemainingRepositories are the selected repositories without a checkout
// outcome, in selection order: what the Work's next start checks out
// (Single checkout, Unfinished checkout).
func (s *AgentWork) RemainingRepositories() []string {
	var remaining []string
	for _, repo := range s.RepositorySelection.Repositories {
		if !slices.ContainsFunc(s.RepositoryCheckouts, func(c RepositoryCheckout) bool { return c.RepositoryID == repo.RepoID }) {
			remaining = append(remaining, repo.RepoID)
		}
	}
	return remaining
}

// RecordRepositoryCheckouts records the checkout outcomes its AgentExecution
// reports for selected repositories still without one (Record repository
// checkouts). A recorded outcome is never replaced (Single checkout,
// Repository degradation), a repository outside the selection is ignored,
// and a Work that entered Closing records nothing (Repository preparation
// cancellation). It reports whether the Work changed.
func (s *AgentWork) RecordRepositoryCheckouts(ended ExecutionCheckoutsEnded) bool {
	if s.Closed() || ended.WorkID != s.ID {
		return false
	}
	remaining := s.RemainingRepositories()
	changed := false
	for _, checkout := range ended.Checkouts {
		if !checkout.valid() || !slices.Contains(remaining, checkout.RepositoryID) {
			continue
		}
		remaining = slices.DeleteFunc(remaining, func(id string) bool { return id == checkout.RepositoryID })
		s.RepositoryCheckouts = append(s.RepositoryCheckouts, checkout)
		changed = true
	}
	if changed {
		s.sortCheckouts()
	}
	return changed
}

// sortCheckouts keeps the recorded outcomes in selection order.
func (s *AgentWork) sortCheckouts() {
	order := make(map[string]int, len(s.RepositorySelection.Repositories))
	for n, repo := range s.RepositorySelection.Repositories {
		order[repo.RepoID] = n
	}
	slices.SortStableFunc(s.RepositoryCheckouts, func(a, b RepositoryCheckout) int { return order[a.RepositoryID] - order[b.RepositoryID] })
}

// recordCheckouts records the checkout outcomes of its run's preparation for
// every repository its LaunchSpecification leaves to it: one the report
// omits did not finish (CheckoutIncomplete), and one outside the run is
// ignored. A run with repositories to check out reports that its checkout
// ended (Record checkouts).
func (e *AgentExecution) recordCheckouts(reported []RepositoryCheckout) {
	if len(e.Launch.RemainingRepositories) == 0 {
		return
	}
	byRepository := make(map[string]RepositoryCheckout, len(reported))
	for _, checkout := range reported {
		byRepository[checkout.RepositoryID] = checkout
	}
	e.CheckoutResults = make([]RepositoryCheckout, 0, len(e.Launch.RemainingRepositories))
	for _, id := range e.Launch.RemainingRepositories {
		checkout, found := byRepository[id]
		if !found {
			checkout = RepositoryCheckout{RepositoryID: id, Reason: CheckoutIncomplete}
		}
		e.CheckoutResults = append(e.CheckoutResults, checkout)
	}
	if ended, ok := e.CheckoutsEnded(); ok {
		e.record(ended)
	}
}

// CheckoutsEnded rebuilds, from its facts, the report that its run's
// checkout ended, so a Work that missed it records the outcomes again
// (Record checkouts). A run that checked nothing out has none.
func (e *AgentExecution) CheckoutsEnded() (ExecutionCheckoutsEnded, bool) {
	if len(e.CheckoutResults) == 0 {
		return ExecutionCheckoutsEnded{}, false
	}
	return ExecutionCheckoutsEnded{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Checkouts: slices.Clone(e.CheckoutResults)}, true
}
