package command

import (
	"context"
	"log/slog"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ResolveStartupCapabilities is only for creation after caller authorization and
// binding-reuse lookup. Existing Works use their retained snapshot instead.
func ResolveStartupCapabilities(ctx context.Context, catalog MarketplacePort, startingUser string, input domain.StartupCapabilityInput) (domain.CapabilitySnapshot, error) {
	return resolveStartupCapabilities(ctx, catalog, startingUser, startingUser, input)
}

func resolveStartupCapabilities(ctx context.Context, catalog MarketplacePort, catalogUser, startingUser string, input domain.StartupCapabilityInput) (domain.CapabilitySnapshot, error) {
	if strings.TrimSpace(catalogUser) == "" {
		return domain.CapabilitySnapshot{}, access.ErrAccessDenied
	}
	var personal []domain.PersonalPlugin
	if startingUser != "" {
		var err error
		personal, err = catalog.ListPersonalPlugins(ctx, startingUser)
		if err != nil {
			return domain.CapabilitySnapshot{}, err
		}
	}
	// Caller-supplied personal records are never an authority for defaults.
	input.PersonalPlugins = personal
	connectors, err := catalog.ReadConnectorSelection(ctx, catalogUser)
	if err != nil {
		return domain.CapabilitySnapshot{}, err
	}
	// The catalog, including required platform integrations, is supplied only by
	// Marketplace; a caller-provided declaration never replaces it. Automated
	// starts have no starting user and therefore no personal Connector defaults.
	input.Connectors = connectors.Catalog
	input.EnabledConnectors = nil
	if startingUser != "" {
		input.EnabledConnectors = connectors.EnabledConnectors
	}
	slugs, err := input.SelectedSlugs()
	if err != nil {
		return domain.CapabilitySnapshot{}, err
	}
	releases := make([]domain.PluginRelease, 0, len(slugs))
	for _, slug := range slugs {
		release, err := catalog.ResolvePlugin(ctx, catalogUser, slug)
		if err != nil {
			return domain.CapabilitySnapshot{}, err
		}
		releases = append(releases, release)
	}
	return domain.NewCapabilitySnapshot(input, releases)
}

// PluginUpgrades reads the current release declarations of each Plugin a
// Work selected, which each Resume or Restart takes while keeping the
// selection (Start upgrade). A Plugin it cannot resolve is left out, so the
// start uses the declarations the Work last took (Unresolvable release).
type PluginUpgrades interface {
	CurrentReleases(ctx context.Context, catalogUser string, capabilities domain.CapabilitySnapshot) ([]domain.PluginRelease, error)
	// StartedWithoutStartupAgent reports an admitted start of work that runs
	// without its dropped startup Agent (Dropped startup Agent warning).
	StartedWithoutStartupAgent(ctx context.Context, work *domain.AgentWork)
}

// CatalogPluginUpgrades resolves current releases from Marketplace's catalog
// as the requesting Owner sees it. An unavailable catalog or a withdrawn
// Plugin degrades to the declarations the Work last took instead of blocking
// the start. Each degradation, and each start that runs without its dropped
// startup Agent, is logged for operators and never shown to the Work's user:
// a release that dropped an Agent still in use calls for the Plugin's
// maintainer, not for the Work's user.
type CatalogPluginUpgrades struct {
	catalog MarketplacePort
	logger  *slog.Logger
}

func NewCatalogPluginUpgrades(catalog MarketplacePort, logger *slog.Logger) *CatalogPluginUpgrades {
	return &CatalogPluginUpgrades{catalog: catalog, logger: logger}
}

func (u *CatalogPluginUpgrades) CurrentReleases(ctx context.Context, catalogUser string, capabilities domain.CapabilitySnapshot) ([]domain.PluginRelease, error) {
	if strings.TrimSpace(catalogUser) == "" {
		return nil, access.ErrAccessDenied
	}
	releases := make([]domain.PluginRelease, 0, len(capabilities.Plugins))
	for _, plugin := range capabilities.Plugins {
		release, err := u.catalog.ResolvePlugin(ctx, catalogUser, plugin.Release.Slug)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			u.logger.WarnContext(ctx, "plugin's current release could not be resolved; the start uses the declarations the Work last took",
				slog.String("plugin", plugin.Release.Slug), slog.String("last_taken_version", plugin.Release.Version), slog.Any("error", err))
			continue
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func (u *CatalogPluginUpgrades) StartedWithoutStartupAgent(ctx context.Context, work *domain.AgentWork) {
	plugins := make([]string, 0, len(work.CapabilitySnapshot.Plugins))
	for _, plugin := range work.CapabilitySnapshot.Plugins {
		plugins = append(plugins, plugin.Release.Slug+"@"+plugin.Release.Version)
	}
	u.logger.WarnContext(ctx, "work starts without its startup Agent; no current release of its Plugins declares it",
		slog.String("work_id", work.ID), slog.String("startup_agent", work.CapabilitySnapshot.StartupAgent), slog.Any("plugins", plugins))
}
