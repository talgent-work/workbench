package agenttest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// catalog is the Marketplace's Plugin and Connector catalog: every user has
// the core plugin enabled, with a sensitive "token" option, and the
// platform Connector; the caller's Plugins resolve beside it.
type catalog struct{ config Config }

func (c *catalog) ListPersonalPlugins(_ context.Context, user string) ([]domain.PersonalPlugin, error) {
	plugin := domain.PersonalPlugin{Slug: "talgent", Enabled: true}
	if c.config.PluginConfiguration != nil {
		plugin.Configuration = pluginconfig.Configuration(c.config.PluginConfiguration(user))
	}
	return []domain.PersonalPlugin{plugin}, nil
}

func (c *catalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	for _, plugin := range c.config.Plugins {
		if plugin.Slug != slug {
			continue
		}
		release := domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: slug, Version: "1", RetrievalRef: "archive:" + slug + "-1", NativeEntry: []byte(`{"name":"` + slug + `","version":"1"}`)}
		for _, agent := range plugin.Agents {
			release.Agents = append(release.Agents, domain.AgentComponent{Name: agent, Description: agent})
		}
		return release, nil
	}
	return domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:runtime-1", NativeEntry: []byte(`{"name":"talgent","version":"1"}`),
		Agents:     []domain.AgentComponent{{Name: "talgent:personal-work-executor", Description: "Personal Work executor"}},
		Parameters: pluginconfig.ParameterDeclarations{"token": {Type: "string", Title: "Token", Description: "Fixture token", Sensitive: true}}}, nil
}

func (*catalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}

// connections are the Provider Authorizations and repositories a user
// reaches.
type connections interface {
	ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error)
	ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error)
}

// startCatalog is the Marketplace a Work's start reaches: the catalog's
// Plugins and Connectors, and the user's connections. It supplies no
// credentials.
type startCatalog struct {
	*catalog
	commandtest.NoCredentials
	connections connections
}

var _ command.MarketplacePort = startCatalog{}

func (s startCatalog) ListPersonalAuthorizations(ctx context.Context, user string) ([]domain.PersonalAuthorization, error) {
	return s.connections.ListPersonalAuthorizations(ctx, user)
}

func (s startCatalog) ListAccessibleRepositories(ctx context.Context, owner string) ([]domain.AccessibleRepository, error) {
	return s.connections.ListAccessibleRepositories(ctx, owner)
}

// fixtureRepositories gives every user one repository under their own
// authorization and no service authorizations.
type fixtureRepositories struct{ authorizationcatalogtest.Empty }

func (fixtureRepositories) ListAccessibleRepositories(_ context.Context, owner string) ([]domain.AccessibleRepository, error) {
	return []domain.AccessibleRepository{{OwnerUserID: owner, Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main"}}, nil
}
