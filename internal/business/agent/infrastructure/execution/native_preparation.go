package execution

import (
	_ "embed"
	"encoding/json"
	"strconv"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

//go:embed native_preparation.cjs
var nativePreparation string

// preparationMinimumSeconds lets a re-attached run past the bound still
// report its finished receipt.
const preparationMinimumSeconds = "30"

// repositoryCheckout is one planned checkout the preparation process performs
// inside the sandbox. It carries no credential: git reaches the Provider
// through the sandbox's Credential Proxy.
type repositoryCheckout struct {
	RepositoryID string `json:"repositoryId"`
	CloneURL     string `json:"cloneUrl"`
	BaseBranch   string `json:"baseBranch"`
	WorkBranch   string `json:"workBranch"`
	TargetPath   string `json:"targetPath"`
}

// preparationCommand prepares the Work's environment before its Harness
// starts: Plugin installation, then checkout of the planned
// repositories. The plan is supplied when the process is created, through
// withCheckoutPlan; without one the start checks nothing out. Only retained
// entries reach the native installer. The Work namespace also isolates
// different retained versions in Claude's HOME-wide package cache.
// The layout decides how the script runs (see machineLayout).
func (c NativeLaunchConfig) preparationCommand(layout launchLayout, request domain.LaunchSpecification, owner SandboxOwner) (string, string, error) {
	name := "talgent-work-" + storageKey(request.WorkID)
	entries := make([]json.RawMessage, 0, len(request.Capabilities.Plugins))
	for _, plugin := range request.Capabilities.Plugins {
		var entry struct{ Name, Version string }
		if err := json.Unmarshal(plugin.Release.NativeEntry, &entry); err != nil || entry.Name != plugin.Release.Slug || (entry.Version != "" && entry.Version != plugin.Release.Version) {
			return "", "", domain.ErrExecutionRequest
		}
		entries = append(entries, plugin.Release.NativeEntry)
	}
	catalog, err := json.Marshal(map[string]any{"name": name, "owner": map[string]string{"name": "Talgent"}, "plugins": entries})
	if err != nil {
		return "", "", err
	}
	// Preparation degrades at the Work's supplied bound. The bound is an
	// absolute moment evaluated when the process runs, so waiting for the
	// sandbox is not granted twice; waiting for the HOME-wide install lock
	// shares it. Closing an observer does not cancel the process; the sandbox
	// owns it until it exits or is reclaimed. Node ends its child process
	// groups before it exits, and timeout waits for Node. The receipt names this
	// execution's report. execd starts a finished session again when it is
	// attached again; the re-run then reports instead of preparing, which the
	// minimum budget leaves time for. Restart uses a new execution and
	// prepares again.
	receipt := storageKey(owner.WorkID + ":" + owner.ID)[:32]
	budget := "remaining=$((" + strconv.FormatInt(request.PreparationBound.Unix(), 10) + " - $(date +%s))); [ \"$remaining\" -ge " + preparationMinimumSeconds + " ] || remaining=" + preparationMinimumSeconds + "; "
	arguments := " " + shellWord(nativePreparation) + " " + shellWord(c.Binary) + " " + shellWord(string(catalog)) + " " + shellWord(receipt) + " \"${talgent_checkout_plan-}\" \"$remaining\""
	return layout.preparation(budget, arguments), name, nil
}

// withCheckoutPlan gives a preparation command the plan of the start it
// prepares.
func withCheckoutPlan(command string, plan []byte) string {
	return "talgent_checkout_plan=" + shellWord(string(plan)) + "; " + command
}
