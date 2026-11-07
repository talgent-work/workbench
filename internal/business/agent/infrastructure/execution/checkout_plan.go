package execution

import (
	"encoding/json"
	"slices"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// checkoutPlan is what the start's preparation process checks out: each
// repository its LaunchSpecification leaves to it, at the target the Work's
// selection plans for it. Nil means nothing to check out.
func checkoutPlan(launch domain.LaunchSpecification) []repositoryCheckout {
	var plan []repositoryCheckout
	for _, checkout := range command.PlanCheckouts(launch.WorkID, launch.RepositorySelection) {
		if slices.Contains(launch.RemainingRepositories, checkout.RepoID) {
			plan = append(plan, repositoryCheckout{RepositoryID: checkout.RepoID, CloneURL: checkout.CloneURL, BaseBranch: checkout.BaseBranch, WorkBranch: checkout.WorkBranch, TargetPath: checkout.TargetPath})
		}
	}
	return plan
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
