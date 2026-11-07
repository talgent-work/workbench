package commandtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// PluginCatalog is the Plugin and Connector part of Marketplace's catalog.
type PluginCatalog interface {
	ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error)
	ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error)
	ResolvePlugin(context.Context, string, string) (domain.PluginRelease, error)
}

// PersonalConnections are the Provider Authorizations and repositories a
// user reaches.
type PersonalConnections interface {
	ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error)
	ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error)
}

// StartCatalog composes a test's stand-ins for the two parts of what
// Marketplace offers a Work's start.
func StartCatalog(plugins PluginCatalog, connections PersonalConnections) command.StartCatalogPort {
	return startCatalog{PluginCatalog: plugins, PersonalConnections: connections}
}

type startCatalog struct {
	PluginCatalog
	PersonalConnections
}
