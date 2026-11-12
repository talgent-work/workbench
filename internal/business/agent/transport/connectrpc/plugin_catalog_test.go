package connectrpc_test

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// The published catalog is a controlled boundary in this RPC lifecycle test.
// Actual Marketplace ACL/auth/persistence is covered by its integration suite.
type rpcPluginCatalog struct{}

func (rpcPluginCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	return []domain.PersonalPlugin{{Slug: "code-review", Enabled: true, Configuration: pluginconfig.Configuration{"mode": "original"}}}, nil
}
func (rpcPluginCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	return domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:" + slug, NativeEntry: []byte(`{"name":"` + slug + `","version":"1"}`), Agents: []domain.AgentComponent{{Name: slug + ":architect"}, {Name: slug + ":reviewer"}}, Parameters: pluginconfig.ParameterDeclarations{"mode": {Type: "string", Title: "Mode", Description: "Mode"}, "language": {Type: "string", Title: "Language", Description: "Language"}}}, nil
}

func (rpcPluginCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
