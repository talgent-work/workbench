package domain

import (
	"slices"
	"strings"
)

// Loading failure reasons of a selected Plugin.
const (
	// PluginNotLoaded: the Harness loaded no release of the Plugin.
	PluginNotLoaded = "not_loaded"
	// PluginVersionMismatch: the Harness loaded another release than the
	// run selected.
	PluginVersionMismatch = "version_mismatch"
)

// PluginOutcome is how one stage of a Plugin's preparation, its installation
// or its loading, ended in a run, as last reported.
type PluginOutcome struct {
	Reported  bool
	Succeeded bool
	// Reason is the failure's safe code; empty on success.
	Reason string
}

// PluginResult is one selected Plugin's preparation outcome in a run (Plugin
// preparation result): its installation and its loading, each independent of
// the other.
type PluginResult struct {
	Slug         string
	Installation PluginOutcome
	Loading      PluginOutcome
}

// PluginInstallation is the in-environment preparation's report of one
// Plugin's installation.
type PluginInstallation struct {
	Slug      string
	Installed bool
	Reason    string
}

// LoadedPlugin is one Plugin release the launched Harness process loaded.
type LoadedPlugin struct{ Slug, Version string }

// PreparationReport reports a run's preparation outcomes. The
// in-environment preparation reports each Plugin's installation and each
// repository's checkout once it ended, naming no process; the launched
// Harness process reports the complete inventory of Plugins it loaded,
// naming itself.
type PreparationReport struct {
	ProcessID     string
	Installations []PluginInstallation
	Checkouts     []RepositoryCheckout
	Loaded        []LoadedPlugin
}

func (r PreparationReport) valid() bool {
	if r.ProcessID == "" && len(r.Loaded) > 0 || r.ProcessID != "" && (len(r.Installations) > 0 || len(r.Checkouts) > 0) {
		return false
	}
	checkedOut := make(map[string]bool, len(r.Checkouts))
	for _, checkout := range r.Checkouts {
		if !checkout.valid() || checkedOut[checkout.RepositoryID] {
			return false
		}
		checkedOut[checkout.RepositoryID] = true
	}
	installed := make(map[string]bool, len(r.Installations))
	for _, installation := range r.Installations {
		if strings.TrimSpace(installation.Slug) == "" || installed[installation.Slug] {
			return false
		}
		installed[installation.Slug] = true
	}
	loaded := make(map[string]bool, len(r.Loaded))
	for _, plugin := range r.Loaded {
		if strings.TrimSpace(plugin.Slug) == "" || loaded[plugin.Slug] {
			return false
		}
		loaded[plugin.Slug] = true
	}
	return true
}

// RecordPreparation records a report of its run's preparation. The
// preparation's report ends its in-environment preparation, records each
// selected Plugin's installation (Preparation order, Preparation ownership,
// Plugin degradation) and each remaining repository's checkout, once
// (Repository degradation); its Harness launches only after that. Its launched process's report records each selected
// Plugin's loading: a Plugin missing from the inventory, or loaded at another
// release, failed to load. A later report replaces a Plugin's outcome (Plugin
// result replacement). A Plugin outside the run's selection, a report from
// another process, which describes another start, and a report to a retired
// execution change nothing.
func (e *AgentExecution) RecordPreparation(report PreparationReport) (bool, error) {
	if !report.valid() {
		return false, ErrInvalidExecution
	}
	if e.Retired() {
		return false, nil
	}
	if report.ProcessID != "" {
		if report.ProcessID != e.Process.ID {
			return false, nil
		}
		return e.recordLoading(report.Loaded), nil
	}
	if e.Environment == "" || e.Process.PreparationEnded {
		return false, nil
	}
	e.Process.PreparationEnded, e.StartupSetback = true, ""
	installed := make(map[string]PluginInstallation, len(report.Installations))
	for _, installation := range report.Installations {
		installed[installation.Slug] = installation
	}
	for _, selected := range e.Launch.Capabilities.Plugins {
		if installation, found := installed[selected.Release.Slug]; found {
			e.recordPlugin(selected.Release.Slug, func(result *PluginResult) {
				result.Installation = PluginOutcome{Reported: true, Succeeded: installation.Installed, Reason: installation.Reason}
			})
		}
	}
	e.recordCheckouts(report.Checkouts)
	return true, nil
}

// recordLoading replaces each selected Plugin's loading from the complete
// inventory its launched process loaded.
func (e *AgentExecution) recordLoading(loaded []LoadedPlugin) bool {
	versions := make(map[string]string, len(loaded))
	for _, plugin := range loaded {
		versions[plugin.Slug] = plugin.Version
	}
	changed := false
	for _, selected := range e.Launch.Capabilities.Plugins {
		outcome := PluginOutcome{Reported: true, Reason: PluginNotLoaded}
		if version, found := versions[selected.Release.Slug]; found {
			outcome.Succeeded, outcome.Reason = version == selected.Release.Version, ""
			if !outcome.Succeeded {
				outcome.Reason = PluginVersionMismatch
			}
		}
		changed = e.recordPlugin(selected.Release.Slug, func(result *PluginResult) { result.Loading = outcome }) || changed
	}
	return changed
}

// recordPlugin applies update to the Plugin's result, adding it when it has
// none yet, and reports whether the result changed.
func (e *AgentExecution) recordPlugin(slug string, update func(*PluginResult)) bool {
	index := slices.IndexFunc(e.PluginResults, func(result PluginResult) bool { return result.Slug == slug })
	if index < 0 {
		e.PluginResults = append(e.PluginResults, PluginResult{Slug: slug})
		index = len(e.PluginResults) - 1
	}
	before := e.PluginResults[index]
	update(&e.PluginResults[index])
	return e.PluginResults[index] != before
}
