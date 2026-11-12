package execution

import (
	_ "embed"
	"encoding/json"
	"fmt"
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
// withCheckoutPlan; without one the start checks nothing out. The Work's
// resolved releases reach the native installer as a local marketplace named
// after their catalog, so every Work of a Harness state shares one
// installation of each release.
// The layout decides how the script runs (see machineLayout).
func (c NativeLaunchConfig) preparationCommand(layout launchLayout, harness domain.AgentHarness, request domain.LaunchSpecification, owner SandboxOwner) (string, error) {
	entries := make([]json.RawMessage, 0, len(request.Capabilities.Plugins))
	for _, plugin := range request.Capabilities.Plugins {
		entry, err := versionedEntry(plugin.Release)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry)
	}
	catalog, err := json.Marshal(map[string]any{"name": request.Capabilities.PluginCatalog(), "owner": map[string]string{"name": "Talgent"}, "plugins": entries})
	if err != nil {
		return "", err
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
	return layout.preparation(harness, budget, arguments), nil
}

// withCheckoutPlan gives a preparation command the plan of the start it
// prepares.
func withCheckoutPlan(command string, plan []byte) string {
	return "talgent_checkout_plan=" + shellWord(string(plan)) + "; " + command
}

// nativeEntryMatches checks that a resolved release's native entry installs
// that release: its name is the release's slug and its version, when it names
// one, the release's. A mismatch is the start's definite failure, naming the
// release and what its entry says; nothing else of the entry is shown.
func nativeEntryMatches(release domain.PluginRelease) error {
	invalid := func(why string) error {
		return domain.InvalidLaunchSpecification(fmt.Sprintf("Plugin release %s %s is invalid: its native entry %s", release.Slug, release.Version, why))
	}
	var entry struct{ Name, Version string }
	switch {
	case json.Unmarshal(release.NativeEntry, &entry) != nil:
		return invalid("is unreadable")
	case entry.Name != release.Slug:
		return invalid("is named " + escaped(entry.Name))
	case entry.Version != "" && entry.Version != release.Version:
		return invalid("is version " + entry.Version)
	}
	return nil
}

// versionedEntry is a resolved release's native entry naming the release's
// version: preparation compares it with the version an install record names,
// so an entry that names none would install the release again on every
// Work.
func versionedEntry(release domain.PluginRelease) (json.RawMessage, error) {
	if err := nativeEntryMatches(release); err != nil {
		return nil, err
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(release.NativeEntry, &entry); err != nil {
		return nil, err
	}
	if _, named := entry["version"]; named {
		return release.NativeEntry, nil
	}
	entry["version"], _ = json.Marshal(release.Version)
	return json.Marshal(entry)
}

// escaped is text from a catalog as a reason shows it: its control
// characters escaped.
func escaped(text string) string {
	quoted := strconv.Quote(text)
	return quoted[1 : len(quoted)-1]
}
