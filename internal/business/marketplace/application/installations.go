package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type InstallationCommand struct {
	ActorUserID string
	Slug        string
	Version     int
}

type InstallPlugin struct {
	ActorUserID   string
	Slug          string
	Configuration map[string]any
}

type SetConfiguration struct {
	InstallationCommand
	Values map[string]any
}

type DeleteConfiguration struct {
	InstallationCommand
	Names []string
}

func (a *Application) GetInstallation(ctx context.Context, userID, slug string) (query.InstalledPlugin, error) {
	if strings.TrimSpace(userID) == "" {
		return query.InstalledPlugin{}, ErrUnauthenticated
	}
	plugin, err := a.installations.Get(ctx, userID, slug)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	return a.presentInstallation(ctx, AssembleInstalledPluginEntity(plugin))
}

func (a *Application) ListInstallations(ctx context.Context, userID, afterSlug string, limit int) (query.InstalledPluginPage, error) {
	if strings.TrimSpace(userID) == "" {
		return query.InstalledPluginPage{}, ErrUnauthenticated
	}
	limit, err := pageLimit(limit)
	if err != nil {
		return query.InstalledPluginPage{}, err
	}
	page, err := a.queries.List(ctx, userID, afterSlug, limit)
	if err != nil {
		return query.InstalledPluginPage{}, err
	}
	for i := range page.Plugins {
		page.Plugins[i], err = a.presentInstallation(ctx, page.Plugins[i])
		if err != nil {
			return query.InstalledPluginPage{}, err
		}
	}
	return page, nil
}

func (a *Application) Install(ctx context.Context, command InstallPlugin) (query.InstalledPlugin, error) {
	if strings.TrimSpace(command.ActorUserID) == "" {
		return query.InstalledPlugin{}, ErrUnauthenticated
	}
	existing, err := a.installations.Get(ctx, command.ActorUserID, command.Slug)
	if err == nil {
		return a.presentInstallation(ctx, AssembleInstalledPluginEntity(existing))
	}
	if !errors.Is(err, domain.ErrInstallationNotFound) {
		return query.InstalledPlugin{}, err
	}
	release, err := a.catalog.Get(ctx, command.Slug)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	plugin, err := domain.Install(command.ActorUserID, command.Slug, AssembleConfigurationDTO(command.Configuration), AssembleParameterDeclarationsDTO(release.Parameters))
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	if err = a.installations.Save(ctx, plugin); err != nil && !errors.Is(err, domain.ErrConcurrentModification) {
		return query.InstalledPlugin{}, err
	}
	// Reload after either insert or a concurrent install. The saved instance's
	// version is stale, and repeated install must not reset existing preferences.
	return a.GetInstallation(ctx, command.ActorUserID, command.Slug)
}

func (a *Application) Enable(ctx context.Context, command InstallationCommand) (query.InstalledPlugin, error) {
	plugin, err := a.loadForChange(ctx, command)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	plugin.Enable()
	return a.save(ctx, plugin)
}

func (a *Application) Disable(ctx context.Context, command InstallationCommand) (query.InstalledPlugin, error) {
	plugin, err := a.loadForChange(ctx, command)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	plugin.Disable()
	return a.save(ctx, plugin)
}

func (a *Application) SetConfiguration(ctx context.Context, command SetConfiguration) (query.InstalledPlugin, error) {
	plugin, err := a.loadForChange(ctx, command.InstallationCommand)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	release, err := a.catalog.Get(ctx, command.Slug)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	if err = plugin.SetConfiguration(AssembleConfigurationDTO(command.Values), AssembleParameterDeclarationsDTO(release.Parameters)); err != nil {
		return query.InstalledPlugin{}, err
	}
	return a.save(ctx, plugin)
}

func (a *Application) DeleteConfiguration(ctx context.Context, command DeleteConfiguration) (query.InstalledPlugin, error) {
	plugin, err := a.loadForChange(ctx, command.InstallationCommand)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	release, err := a.catalog.Get(ctx, command.Slug)
	if err != nil {
		return query.InstalledPlugin{}, err
	}
	if err = plugin.DeleteConfiguration(command.Names, AssembleParameterDeclarationsDTO(release.Parameters)); err != nil {
		return query.InstalledPlugin{}, err
	}
	return a.save(ctx, plugin)
}

func (a *Application) Uninstall(ctx context.Context, command InstallationCommand) error {
	plugin, err := a.loadForChange(ctx, command)
	if errors.Is(err, domain.ErrInstallationNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	plugin.Uninstall()
	return a.installations.Delete(ctx, plugin)
}

func (a *Application) loadForChange(ctx context.Context, command InstallationCommand) (*domain.InstalledPlugin, error) {
	if strings.TrimSpace(command.ActorUserID) == "" {
		return nil, ErrUnauthenticated
	}
	plugin, err := a.installations.Get(ctx, command.ActorUserID, command.Slug)
	if err != nil {
		return nil, err
	}
	if command.Version != plugin.Version {
		return nil, domain.ErrConcurrentModification
	}
	return plugin, nil
}

func (a *Application) save(ctx context.Context, plugin *domain.InstalledPlugin) (query.InstalledPlugin, error) {
	if err := a.installations.Save(ctx, plugin); err != nil {
		return query.InstalledPlugin{}, err
	}
	return a.GetInstallation(ctx, plugin.UserID, plugin.Slug)
}
