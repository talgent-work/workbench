package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

type pluginInstallationResult struct {
	Slug      string `json:"slug"`
	Installed bool   `json:"installed"`
	Reason    string `json:"reason"`
}

// discardPluginPreparation terminates and forgets an unfinished preparation
// once the Work asked its process to exit. This does not establish Harness
// exit or successful plugin preparation. If the record is lost, the next
// withdrawal repeats deletion of the same handle before clearing it.
func (n *NativeConnections) discardPluginPreparation(ctx context.Context, record *executionRecord, client *execd.Client) error {
	if record.PreparationProcessID == "" || record.PreparationEnded {
		return nil
	}
	// Native installation uses child process groups. Let the preparation
	// adapter terminate those children before execd removes the parent handle.
	if err := client.Terminate(ctx, record.PreparationProcessID); err != nil {
		return err
	}
	if err := n.provisioning.technical(ctx, *record, "preparation_process_id = ''"); err != nil {
		return err
	}
	record.PreparationProcessID = ""
	return nil
}

// execdReadiness bounds the wait for a fresh environment's execd, which starts
// about a second after its sandbox runs. It stays well inside the Startup
// Deadline; an execd that has not answered by then fails the attempt, naming
// it, for the step's bounded retry.
const execdReadiness = time.Minute

// startPreparation creates the preparation process with the start's checkout
// plan: the repositories its LaunchSpecification leaves to it, fixed when the
// process is created. It is the first command into a fresh environment, so it
// waits for execd to answer first.
func (n *NativeConnections) startPreparation(ctx context.Context, record *executionRecord, client *execd.Client, launch domain.LaunchSpecification) error {
	if record.PreparationCommand == "" {
		return domain.ErrExecutionRequest
	}
	encoded, err := json.Marshal(checkoutPlan(launch))
	if err != nil {
		return err
	}
	if err := client.AwaitReady(ctx, execdReadiness); err != nil {
		return err
	}
	id, err := client.Create(ctx, "/workspace", withCheckoutPlan(record.PreparationCommand, encoded))
	if err != nil {
		return err
	}
	if err := n.provisioning.technical(ctx, *record, "preparation_process_id = ?, checkout_plan = ?", id, encoded); err != nil {
		return err
	}
	record.PreparationProcessID, record.CheckoutPlan = id, encoded
	return nil
}

// prepareEnvironment attaches to the separately reserved preparation process,
// which installs Plugins and then checks out the planned repositories before
// the Harness starts, and waits for its exit until ctx ends. An
// interrupted wait detaches only; the retained handle is reattached from
// offset zero. It reports each selected Plugin's installation and each
// planned repository's checkout, which the execution records with the end
// of its preparation, so a ready Harness always follows recorded outcomes.
func (n *NativeConnections) prepareEnvironment(ctx context.Context, record *executionRecord, client *execd.Client, launch domain.LaunchSpecification) (domain.PreparationReport, error) {
	var none domain.PreparationReport
	if record.PreparationEnded {
		return none, nil
	}
	if record.PreparationProcessID == "" {
		if err := n.startPreparation(ctx, record, client, launch); err != nil {
			return none, err
		}
	}
	snapshot := launch.Capabilities
	plan, err := record.checkoutPlan()
	if err != nil {
		return none, err
	}
	pipe, err := client.Attach(ctx, record.PreparationProcessID, 0)
	if err != nil {
		return none, err
	}
	defer pipe.Close()
	var output []byte
	for {
		frame, err := pipe.Receive(ctx)
		if err != nil {
			return none, err
		}
		switch frame.Type {
		case "stdout", "replay":
			if len(output)+len(frame.Data) > 128*1024 {
				return none, errors.New("agent: preparation report too large")
			}
			output = append(output, frame.Data...)
		case "exit":
			report := latestReport(output, *frame.ExitCode, snapshot, plan)
			if report.Repositories == nil {
				report.Repositories = settledCheckouts(output, plan)
			}
			// The wait may outlast other steps; it ended the preparation process
			// still recorded for the execution.
			current, found, err := n.provisioning.get(ctx, record.ID)
			if err != nil {
				return none, err
			}
			if !found || current.PreparationProcessID != record.PreparationProcessID {
				return none, errEnvironmentConflict
			}
			return domain.PreparationReport{Installations: report.plugins(snapshot), Checkouts: report.checkouts(plan, unfinishedCheckout(*frame.ExitCode))}, nil
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

// plugins reports each selected Plugin's installation; without a usable
// report, every Plugin failed to prepare.
func (r preparationReport) plugins(snapshot domain.CapabilitySnapshot) []domain.PluginInstallation {
	seen := validPlugins(r.Results, snapshot)
	results := make([]domain.PluginInstallation, 0, len(snapshot.Plugins))
	for _, plugin := range snapshot.Plugins {
		result, found := seen[plugin.Release.Slug]
		if !found {
			result = pluginInstallationResult{Slug: plugin.Release.Slug, Reason: "preparation_failed"}
		}
		results = append(results, domain.PluginInstallation{Slug: result.Slug, Installed: result.Installed, Reason: result.Reason})
	}
	return results
}

// checkouts translates the reported outcome of each planned repository, in
// plan order. A reason outside the safe vocabulary is never shown or
// retried; a planned repository the report omits did not finish, for the
// unfinished reason.
func (r preparationReport) checkouts(plan []repositoryCheckout, unfinished string) []domain.RepositoryCheckout {
	reported := make(map[string]checkoutReport, len(r.Repositories))
	for _, outcome := range r.Repositories {
		reported[outcome.RepositoryID] = outcome
	}
	results := make([]domain.RepositoryCheckout, 0, len(plan))
	for _, planned := range plan {
		outcome, found := reported[planned.RepositoryID]
		result := domain.RepositoryCheckout{RepositoryID: planned.RepositoryID, CheckedOut: found && outcome.CheckedOut, Reason: unfinished}
		switch {
		case result.CheckedOut:
			result.Reason = ""
		case !found:
		case domain.IsCheckoutReason(outcome.Reason):
			result.Reason = outcome.Reason
		default:
			result.Reason = domain.CheckoutUnavailable
		}
		results = append(results, result)
	}
	return results
}

// unfinishedCheckout is how a repository still checking out when the process
// ended finished: at the preparation bound when timeout ended it.
func unfinishedCheckout(exit int) string {
	if exit == timeoutExitCode || exit == timeoutKilledExitCode {
		return domain.CheckoutTimedOut
	}
	return domain.CheckoutIncomplete
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
