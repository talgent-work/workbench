package execution

import (
	"context"
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// checkoutPlan maps the current start's checkout plan for its preparation
// process. Nil means nothing to check out.
func (p *Provisioning) checkoutPlan(ctx context.Context, workID string) ([]repositoryCheckout, error) {
	request, found, err := p.initialRequest(ctx, workID)
	if err != nil || !found {
		return nil, err
	}
	checkouts, pending := request.CurrentCheckoutPlan()
	if pending {
		return nil, command.ErrInitialExecutionPending
	}
	if checkouts == nil {
		return nil, nil
	}
	plan := make([]repositoryCheckout, 0, len(checkouts))
	for _, checkout := range checkouts {
		plan = append(plan, repositoryCheckout{RepositoryID: checkout.RepoID, CloneURL: checkout.CloneURL, BaseBranch: checkout.BaseBranch, WorkBranch: checkout.WorkBranch, TargetPath: checkout.TargetPath})
	}
	return plan, nil
}

func (r allocationRecord) checkoutPlan() ([]repositoryCheckout, error) {
	if len(r.CheckoutPlan) == 0 {
		return nil, nil
	}
	var plan []repositoryCheckout
	if err := json.Unmarshal(r.CheckoutPlan, &plan); err != nil {
		return nil, oops.With("operation", "decode_checkout_plan").Wrap(err)
	}
	return plan, nil
}
