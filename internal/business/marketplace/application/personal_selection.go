package application

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

// ReadPersonalSelection is restricted by the trusted execution transport. It
// keeps Sensitive values out of ordinary personal installation responses.
func (a *Application) ReadPersonalSelection(ctx context.Context, owner, cursor string, limit int) (query.InstalledPluginPage, error) {
	if strings.TrimSpace(owner) == "" {
		return query.InstalledPluginPage{}, ErrUnauthenticated
	}
	limit, err := pageLimit(limit)
	if err != nil {
		return query.InstalledPluginPage{}, err
	}
	return a.queries.List(ctx, owner, cursor, limit)
}
func (a *Application) presentInstallation(ctx context.Context, plugin query.InstalledPlugin) (query.InstalledPlugin, error) {
	release, err := a.catalog.Get(ctx, plugin.Slug)
	if err != nil && !errors.Is(err, ErrReleaseNotFound) && !errors.Is(err, ErrCatalogNotReady) {
		return query.InstalledPlugin{}, err
	}
	visible := make(map[string]any, len(plugin.Configuration))
	for name, value := range plugin.Configuration {
		declaration, known := release.Parameters[name]
		if !known || declaration.Sensitive {
			plugin.RedactedParameters = append(plugin.RedactedParameters, name)
			continue
		}
		visible[name] = value
	}
	sort.Strings(plugin.RedactedParameters)
	plugin.Configuration = visible
	return plugin, nil
}
