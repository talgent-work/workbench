package command

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Bounds of a Plugin inventory a Harness process reports.
const (
	maxLoadedPlugins      = 4096
	maxPluginSlugBytes    = 128
	maxPluginVersionBytes = 256
)

// recordPluginLoads records on the execution each selected Plugin's loading
// from the complete inventory its launched process reported. An inventory
// from another process describes another start and changes nothing; it never
// changes the Work.
func (h *ApplyExecutionFeedbackHandler) recordPluginLoads(ctx context.Context, execution *domain.AgentExecution, feedback ExecutionFeedback) error {
	if !validInventory(feedback.LoadedPlugins) {
		return domain.ErrInvalidWork
	}
	if !launched(execution, feedback.ProcessID) {
		return nil
	}
	changed, err := execution.RecordPreparation(domain.PreparationReport{ProcessID: feedback.ProcessID, Loaded: feedback.LoadedPlugins})
	if err != nil || !changed {
		return err
	}
	return h.executions.Save(ctx, execution)
}

// validInventory accepts a bounded inventory naming each Plugin once.
func validInventory(loaded []domain.LoadedPlugin) bool {
	if len(loaded) > maxLoadedPlugins {
		return false
	}
	seen := make(map[string]bool, len(loaded))
	for _, plugin := range loaded {
		if strings.TrimSpace(plugin.Slug) == "" || len(plugin.Slug) > maxPluginSlugBytes || len(plugin.Version) > maxPluginVersionBytes || seen[plugin.Slug] {
			return false
		}
		seen[plugin.Slug] = true
	}
	return true
}
