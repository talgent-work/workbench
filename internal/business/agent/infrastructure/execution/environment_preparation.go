package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

type pluginInstallationResult struct {
	Slug      string `json:"slug"`
	Installed bool   `json:"installed"`
	Reason    string `json:"reason"`
}

// Discard only the fixed preparation handle while the Work is draining. This
// does not establish Harness exit or successful plugin preparation. If the
// commit is lost, recovery repeats deletion of the same handle before clearing it.
func (n *NativeConnections) discardPluginPreparation(ctx context.Context, record *allocationRecord, client *execd.Client) error {
	if record.PreparationProcessID == "" || record.EnvironmentPrepared {
		return nil
	}
	// Native installation uses child process groups. Let the preparation
	// adapter terminate those children before execd removes the parent handle.
	if err := client.Terminate(ctx, record.PreparationProcessID); err != nil {
		return err
	}
	if err := n.update(ctx, *record, "preparation_process_id = ''"); err != nil {
		return err
	}
	record.PreparationProcessID = ""
	record.Version++
	return nil
}

// startPreparation creates the preparation process with the start's checkout
// plan. The plan is fixed when the process is created: the first start's plan
// is determined before its credentials reach the sandbox.
func (n *NativeConnections) startPreparation(ctx context.Context, record *allocationRecord, client *execd.Client) error {
	if record.PreparationCommand == "" {
		return domain.ErrExecutionRequest
	}
	plan, err := n.provisioning.checkoutPlan(ctx, record.SessionID)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	id, err := client.Create(ctx, "/workspace", withCheckoutPlan(record.PreparationCommand, encoded))
	if err != nil {
		return err
	}
	if err := n.update(ctx, *record, "preparation_process_id = ?, checkout_plan = ?", id, encoded); err != nil {
		return err
	}
	record.PreparationProcessID, record.CheckoutPlan = id, encoded
	record.Version++
	return nil
}

// prepareEnvironment attaches to the separately reserved preparation process,
// which installs Plugins and then checks out the planned repositories before
// the Harness starts, and waits for its exit until ctx ends. An
// interrupted wait detaches only; the retained handle is reattached from
// offset zero. Outcomes are recorded before preparation counts as finished,
// so a ready Harness always follows recorded checkout outcomes.
func (n *NativeConnections) prepareEnvironment(ctx context.Context, record *allocationRecord, client *execd.Client, snapshot domain.CapabilitySnapshot) error {
	if record.EnvironmentPrepared {
		return nil
	}
	if record.PreparationProcessID == "" {
		if err := n.startPreparation(ctx, record, client); err != nil {
			return err
		}
	}
	plan, err := record.checkoutPlan()
	if err != nil {
		return err
	}
	pipe, err := client.Attach(ctx, record.PreparationProcessID, 0)
	if err != nil {
		return err
	}
	defer pipe.Close()
	var output []byte
	for {
		frame, err := pipe.Receive(ctx)
		if err != nil {
			return err
		}
		switch frame.Type {
		case "stdout", "replay":
			if len(output)+len(frame.Data) > 128*1024 {
				return errors.New("agent: preparation report too large")
			}
			output = append(output, frame.Data...)
		case "exit":
			report := latestReport(output, *frame.ExitCode, snapshot, plan)
			if report.Repositories == nil {
				report.Repositories = settledCheckouts(output, plan)
			}
			// Idempotent Work/slug projection, independent of Work lifecycle. A
			// late result is allowed to be superseded by current preparation facts.
			if err := n.savePluginResults(ctx, record.SessionID, report.plugins(snapshot)); err != nil {
				return err
			}
			if plan != nil {
				if err := n.checkouts.Handle(ctx, command.RecordRepositoryCheckouts{WorkID: record.SessionID, Results: report.checkouts(), Unfinished: unfinishedCheckout(*frame.ExitCode)}); err != nil {
					return err
				}
			}
			// The wait may outlast other writes to the allocation; finishing
			// applies to the same preparation process at its current version.
			current, err := n.current(ctx, *record)
			if err != nil {
				return err
			}
			if current.PreparationProcessID != record.PreparationProcessID {
				return errEnvironmentConflict
			}
			if err := n.update(ctx, current, "plugins_prepared = 1"); err != nil {
				return err
			}
			*record = current
			record.EnvironmentPrepared = true
			record.Version++
			return nil
		}
	}
}

// preparationReport is the preparation process's report of one run.
type preparationReport struct {
	Results      []pluginInstallationResult `json:"results"`
	Repositories []checkoutReport           `json:"repositories"`
}

type checkoutReport struct {
	RepositoryID string `json:"repositoryId"`
	TargetPath   string `json:"targetPath"`
	CheckedOut   bool   `json:"checkedOut"`
	Reason       string `json:"reason"`
}

// latestReport trusts the latest complete report of a successful exit. execd
// re-runs a finished preparation when it is attached again and replays every
// run's output, so the output may hold several reports; each run re-applies
// the same Work selection, and the last describes the current state. Without
// one, every Plugin and planned repository failed to prepare.
func latestReport(output []byte, exit int, snapshot domain.CapabilitySnapshot, plan []repositoryCheckout) preparationReport {
	if exit != 0 {
		return preparationReport{}
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var report preparationReport
		if json.Unmarshal([]byte(lines[i]), &report) == nil && validPlugins(report.Results, snapshot) != nil && validCheckouts(report.Repositories, plan) {
			return report
		}
	}
	return preparationReport{}
}

func (r preparationReport) plugins(snapshot domain.CapabilitySnapshot) []pluginInstallationResult {
	seen := validPlugins(r.Results, snapshot)
	results := make([]pluginInstallationResult, 0, len(snapshot.Plugins))
	for _, plugin := range snapshot.Plugins {
		result, found := seen[plugin.Release.Slug]
		if !found {
			result = pluginInstallationResult{Slug: plugin.Release.Slug, Reason: "preparation_failed"}
		}
		results = append(results, result)
	}
	return results
}

// checkouts translates the reported outcomes. A reason outside the safe
// vocabulary is never shown or retried; omitted repositories are completed by
// the Application from the plan.
func (r preparationReport) checkouts() []command.RepositoryCheckoutResult {
	results := make([]command.RepositoryCheckoutResult, 0, len(r.Repositories))
	for _, outcome := range r.Repositories {
		result := command.RepositoryCheckoutResult{RepositoryID: outcome.RepositoryID, TargetPath: outcome.TargetPath, Prepared: outcome.CheckedOut, Reason: outcome.Reason}
		if !outcome.CheckedOut && !command.IsCheckoutReason(outcome.Reason) {
			result.Reason = command.CheckoutUnavailable
		}
		results = append(results, result)
	}
	return results
}

// unfinishedCheckout is how a repository still checking out when the process
// ended finished: at the preparation bound when timeout ended it.
func unfinishedCheckout(exit int) string {
	if exit == timeoutExitCode || exit == timeoutKilledExitCode {
		return command.CheckoutTimedOut
	}
	return command.CheckoutIncomplete
}

// Exit codes of coreutils timeout: the command timed out, or had to be killed.
const (
	timeoutExitCode       = 124
	timeoutKilledExitCode = 137
)

// validCheckouts accepts outcomes for exactly the planned repositories, each
// at its planned path; a start without a plan reports none.
func validCheckouts(outcomes []checkoutReport, plan []repositoryCheckout) bool {
	if len(outcomes) != len(plan) {
		return false
	}
	planned := plannedTargets(plan)
	for _, outcome := range outcomes {
		if !validCheckout(outcome, planned) {
			return false
		}
		delete(planned, outcome.RepositoryID)
	}
	return true
}

// settledCheckouts collects the per-repository lines a preparation process
// writes as each planned repository settles. They stand in for the report of a
// process that ended before writing it; the latest line per repository wins.
func settledCheckouts(output []byte, plan []repositoryCheckout) []checkoutReport {
	planned := plannedTargets(plan)
	latest := map[string]checkoutReport{}
	for _, line := range strings.Split(string(output), "\n") {
		var settled struct {
			Checkout *checkoutReport `json:"checkout"`
		}
		if json.Unmarshal([]byte(line), &settled) == nil && settled.Checkout != nil && validCheckout(*settled.Checkout, planned) {
			latest[settled.Checkout.RepositoryID] = *settled.Checkout
		}
	}
	outcomes := make([]checkoutReport, 0, len(latest))
	for _, checkout := range plan {
		if outcome, found := latest[checkout.RepositoryID]; found {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes
}

func plannedTargets(plan []repositoryCheckout) map[string]string {
	planned := make(map[string]string, len(plan))
	for _, checkout := range plan {
		planned[checkout.RepositoryID] = checkout.TargetPath
	}
	return planned
}

// validCheckout accepts one planned repository's outcome at its planned path.
func validCheckout(outcome checkoutReport, planned map[string]string) bool {
	target, found := planned[outcome.RepositoryID]
	return found && target == outcome.TargetPath && (outcome.CheckedOut == (outcome.Reason == ""))
}

// validPlugins accepts one report covering every selected plugin exactly once
// with a known outcome, or returns nil.
func validPlugins(reported []pluginInstallationResult, snapshot domain.CapabilitySnapshot) map[string]pluginInstallationResult {
	if len(reported) != len(snapshot.Plugins) {
		return nil
	}
	seen := map[string]pluginInstallationResult{}
	for _, result := range reported {
		if _, exists := seen[result.Slug]; exists {
			return nil
		}
		if (result.Installed && result.Reason != "") || (!result.Installed && result.Reason != "installation_failed" && result.Reason != "marketplace_unavailable") {
			return nil
		}
		seen[result.Slug] = result
	}
	for _, plugin := range snapshot.Plugins {
		if _, found := seen[plugin.Release.Slug]; !found {
			return nil
		}
	}
	return seen
}

func (n *NativeConnections) savePluginResults(ctx context.Context, workID string, results []pluginInstallationResult) error {
	now := n.provisioning.now().UnixMilli()
	for _, result := range results {
		_, err := n.provisioning.db.Context(ctx).Exec(`INSERT INTO agent_plugin_result
		(id, work_id, slug, installation_observed, installed, reason, version, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, 1, ?, ?, 1, ?, ?, 0)
		ON DUPLICATE KEY UPDATE installation_observed=1, installed=VALUES(installed), reason=VALUES(reason), version=version+1, updated_at=VALUES(updated_at)`,
			uuid.Must(uuid.NewV7()).String(), workID, result.Slug, result.Installed, result.Reason, now, now)
		if err != nil {
			return oops.With("operation", "save_plugin_installation_result").Wrap(err)
		}
	}
	return nil
}
