package agent

import (
	"net/http"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"go.uber.org/fx"
)

type PluginCatalogConfig = plugincatalog.Config

var pluginCatalogModule = fx.Options(fx.Provide(fx.Annotate(func(client *http.Client, config PluginCatalogConfig) (*plugincatalog.Client, error) {
	return plugincatalog.NewClient(client, config)
}, fx.As(new(command.PluginCatalogPort)), fx.As(new(command.PersonalConnectionsPort)), fx.As(new(query.AccessibleRepositoriesPort)), fx.As(new(application.RepositoryCredentialPort)), fx.As(new(application.ServiceCredentialPort)))))
