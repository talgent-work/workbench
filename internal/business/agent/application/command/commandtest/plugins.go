package commandtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// UnchangedPlugins reports every selected Plugin's retained release as current,
// for tests whose start is not about upgrading Plugins.
type UnchangedPlugins struct{}

var _ command.PluginUpgrades = UnchangedPlugins{}

func (UnchangedPlugins) CurrentReleases(_ context.Context, _ string, capabilities domain.CapabilitySnapshot) ([]domain.PluginRelease, error) {
	releases := make([]domain.PluginRelease, 0, len(capabilities.Plugins))
	for _, plugin := range capabilities.Plugins {
		releases = append(releases, plugin.Release.Clone())
	}
	return releases, nil
}
