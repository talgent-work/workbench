package command

import (
	"context"
	"errors"
)

// Safe checkout reason codes reported by the Work's preparation process. Raw
// Git diagnostics never leave the sandbox.
const (
	CheckoutUnavailable   = "checkout_unavailable"
	CheckoutTimedOut      = "timed_out"
	CheckoutIncomplete    = "preparation_incomplete"
	CheckoutBranchMissing = "branch_unavailable"
	CheckoutTargetTaken   = "target_conflict"
)

// IsCheckoutReason reports whether reason is one of the safe codes above.
func IsCheckoutReason(reason string) bool {
	switch reason {
	case CheckoutUnavailable, CheckoutTimedOut, CheckoutIncomplete, CheckoutBranchMissing, CheckoutTargetTaken:
		return true
	}
	return false
}

var errInconsistentCheckouts = errors.New("agent: reported checkouts do not match the retained plan")

// RecordRepositoryCheckouts carries the outcome of the planned checkouts, as
// reported by the preparation process of the Work's current start.
type RecordRepositoryCheckouts struct {
	WorkID  string
	Results []RepositoryCheckoutResult
	// Unfinished is the reason of every planned repository the report omits:
	// CheckoutTimedOut when the preparation bound ended the process, otherwise
	// CheckoutIncomplete.
	Unfinished string
}

// RecordRepositoryCheckoutsHandler ends the first start's checkout step. Every
// planned repository ends checked out or not checked out with its reason; a
// later report never replaces the recorded outcome (Repository degradation).
type RecordRepositoryCheckoutsHandler struct{ requests InitialExecutionRequests }

func NewRecordRepositoryCheckoutsHandler(requests InitialExecutionRequests) *RecordRepositoryCheckoutsHandler {
	return &RecordRepositoryCheckoutsHandler{requests: requests}
}

func (h *RecordRepositoryCheckoutsHandler) Handle(ctx context.Context, input RecordRepositoryCheckouts) error {
	request, err := h.requests.Get(ctx, input.WorkID)
	if errors.Is(err, ErrInitialExecutionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if open, pending := request.checkoutStep(); !open || pending {
		return nil
	}
	unfinished := CheckoutIncomplete
	if input.Unfinished == CheckoutTimedOut {
		unfinished = CheckoutTimedOut
	}
	results, err := request.checkoutOutcomes(input.Results, unfinished)
	if err != nil {
		return err
	}
	request.CheckoutResults = results
	request.InputPrepared = true
	request.RecoveryPending = false
	return h.requests.Save(ctx, request)
}

// checkoutStep reports whether the current start has an open checkout step:
// the first start's checkout until it ends, so a first start that ended before
// checkout finished completes it on the next start (Resume or Restart). A
// rejected start, or one after the checkout ended, has none and keeps existing
// checkouts. The plan is fixed before the first start's credentials reach the
// sandbox, so an open step is pending until the request is prepared.
func (r InitialExecutionRequest) checkoutStep() (open, pending bool) {
	open = !r.Rejected && !r.InputPrepared
	return open, open && !r.Prepared
}

// CurrentCheckoutPlan is what the current start's preparation checks out. Nil
// means the start has no checkout step; an empty plan is a step with no
// repositories, which still ends the step.
func (r InitialExecutionRequest) CurrentCheckoutPlan() (plan []RepositoryCheckout, pending bool) {
	open, pending := r.checkoutStep()
	if !open || pending {
		return nil, pending
	}
	return append([]RepositoryCheckout{}, r.Checkouts...), false
}

// checkoutOutcomes orders reported results by the retained plan. A repository
// the report omits did not finish checking out, for the unfinished reason.
func (r InitialExecutionRequest) checkoutOutcomes(reported []RepositoryCheckoutResult, unfinished string) ([]RepositoryCheckoutResult, error) {
	byRepository := make(map[string]RepositoryCheckoutResult, len(reported))
	for _, result := range reported {
		if _, duplicate := byRepository[result.RepositoryID]; duplicate || !validCheckoutOutcome(result) {
			return nil, errInconsistentCheckouts
		}
		byRepository[result.RepositoryID] = result
	}
	outcomes := make([]RepositoryCheckoutResult, 0, len(r.Checkouts))
	for _, checkout := range r.Checkouts {
		result, found := byRepository[checkout.RepoID]
		delete(byRepository, checkout.RepoID)
		if !found {
			result = RepositoryCheckoutResult{RepositoryID: checkout.RepoID, Reason: unfinished}
		}
		if result.TargetPath != "" && result.TargetPath != checkout.TargetPath {
			return nil, errInconsistentCheckouts
		}
		result.TargetPath = checkout.TargetPath
		outcomes = append(outcomes, result)
	}
	if len(byRepository) > 0 {
		return nil, errInconsistentCheckouts
	}
	return outcomes, nil
}

func validCheckoutOutcome(result RepositoryCheckoutResult) bool {
	if result.Prepared {
		return result.Reason == ""
	}
	return IsCheckoutReason(result.Reason)
}
