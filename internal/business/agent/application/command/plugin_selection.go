package command

import (
	"context"
	"log/slog"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StartCatalogPort reaches what Marketplace offers a Work's start, under its
// trusted owner scope: the Connector catalog and personal selection, Plugin
// release resolution, Provider Authorizations and the repositories reachable
// under them. Startup never changes personal installations, and the catalog
// never supplies credentials or grants continuing access.
type StartCatalogPort interface {
	ReadConnectorSelection(context.Context, string) (ConnectorSelection, error)
	ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error)
	ResolvePlugin(context.Context, string, string) (domain.PluginRelease, error)
	ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error)
	ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error)
}

// ConnectorSelection is Marketplace's catalog with the delegated user's enabled
// installation identities.
type ConnectorSelection struct {
	Catalog           []domain.CatalogConnector
	EnabledConnectors []string
}

// ResolveStartupCapabilities is only for creation after caller authorization and
// binding-reuse lookup. Existing Works use their retained snapshot instead.
func ResolveStartupCapabilities(ctx context.Context, catalog StartCatalogPort, startingUser string, input domain.StartupCapabilityInput) (domain.CapabilitySnapshot, error) {
	return resolveStartupCapabilities(ctx, catalog, startingUser, startingUser, input)
}

func resolveStartupCapabilities(ctx context.Context, catalog StartCatalogPort, catalogUser, startingUser string, input domain.StartupCapabilityInput) (domain.CapabilitySnapshot, error) {
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

// PluginUpgrades reads the current release of each selected Plugin, which a
// Resume or Restart takes up while keeping the selection. A Plugin it cannot
// resolve is left out, so the Work keeps its retained release.
type PluginUpgrades interface {
	CurrentReleases(ctx context.Context, catalogUser string, selected []domain.SelectedPlugin) ([]domain.PluginRelease, error)
}

// CatalogPluginUpgrades resolves current releases from Marketplace's catalog
// as the requesting Owner sees it. An unavailable catalog or a withdrawn
// Plugin degrades to the retained release instead of blocking the start.
type CatalogPluginUpgrades struct {
	catalog StartCatalogPort
	logger  *slog.Logger
}

func NewCatalogPluginUpgrades(catalog StartCatalogPort, logger *slog.Logger) *CatalogPluginUpgrades {
	return &CatalogPluginUpgrades{catalog: catalog, logger: logger}
}

func (u *CatalogPluginUpgrades) CurrentReleases(ctx context.Context, catalogUser string, selected []domain.SelectedPlugin) ([]domain.PluginRelease, error) {
	if strings.TrimSpace(catalogUser) == "" {
		return nil, access.ErrAccessDenied
	}
	releases := make([]domain.PluginRelease, 0, len(selected))
	for _, plugin := range selected {
		release, err := u.catalog.ResolvePlugin(ctx, catalogUser, plugin.Release.Slug)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			u.logger.WarnContext(ctx, "plugin keeps its retained release; its current release could not be resolved",
				slog.String("plugin", plugin.Release.Slug), slog.String("retained_version", plugin.Release.Version), slog.Any("error", err))
			continue
		}
		releases = append(releases, release)
	}
	return releases, nil
}
