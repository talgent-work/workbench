package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type PluginLoad struct{ Slug, Version string }

type PluginLoadResult struct {
	Slug   string
	Loaded bool
	Reason string
}

// PluginResults retains per-Work projection facts. Installation and loading
// writers own separate columns, and late feedback may replace earlier facts.
type PluginResults interface {
	query.PluginResultReader
	RecordLoads(context.Context, string, []PluginLoadResult) error
}

func (a *Application) GetWorkPluginResults(ctx context.Context, input ControlWork) ([]query.PluginResult, error) {
	if strings.TrimSpace(input.WorkID) == "" {
		return nil, domain.ErrInvalidWork
	}
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return nil, err
	}
	work, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return nil, err
	}
	observations, err := a.PluginResults.Read(ctx, work.ID)
	if err != nil {
		return nil, err
	}
	return AssembleWorkPluginResults(work, observations), nil
}

// RecordPluginLoads consumes an authenticated, complete native inventory. It
// neither changes the Work nor infers loading from installation success.
func (a *Application) RecordPluginLoads(ctx context.Context, workID string, plugins []PluginLoad) error {
	if strings.TrimSpace(workID) == "" || len(plugins) > 4096 {
		return domain.ErrInvalidWork
	}
	loaded := make(map[string]string, len(plugins))
	for _, plugin := range plugins {
		if strings.TrimSpace(plugin.Slug) == "" || len(plugin.Slug) > 128 || len(plugin.Version) > 256 {
			return domain.ErrInvalidWork
		}
		if _, exists := loaded[plugin.Slug]; exists {
			return domain.ErrInvalidWork
		}
		loaded[plugin.Slug] = plugin.Version
	}
	work, err := a.Works.Get(ctx, workID)
	if err != nil {
		return err
	}
	results := make([]PluginLoadResult, 0, len(work.CapabilitySnapshot.Plugins))
	for _, selected := range work.CapabilitySnapshot.Plugins {
		result := PluginLoadResult{Slug: selected.Release.Slug, Reason: "not_loaded"}
		if version, found := loaded[result.Slug]; found {
			if version == selected.Release.Version {
				result.Loaded, result.Reason = true, ""
			} else {
				result.Reason = "version_mismatch"
			}
		}
		results = append(results, result)
	}
	return a.PluginResults.RecordLoads(ctx, workID, results)
}
