package agent

import (
	"net/http"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"go.uber.org/fx"
)

type PluginCatalogConfig = plugincatalog.Config

var pluginCatalogModule = fx.Options(fx.Provide(fx.Annotate(func(client *http.Client, config PluginCatalogConfig) (*plugincatalog.Client, error) {
	return plugincatalog.NewClient(client, config)
}, fx.As(new(command.StartCatalogPort)), fx.As(new(application.CredentialSupplyPort)))))
