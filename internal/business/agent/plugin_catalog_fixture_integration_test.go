//go:build integration

package agent_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"go.uber.org/fx"
)

// Lifecycle module tests control the external catalog, not Agent selection or
// snapshot assembly. Marketplace API/native catalog journeys test that boundary.
type modulePluginCatalog struct{}

func moduleCatalog() fx.Option {
	return fx.Replace(fx.Annotate(modulePluginCatalog{}, fx.As(new(command.PluginCatalogPort))), fx.Annotate(authorizationcatalogtest.Empty{}, fx.As(new(command.PersonalConnectionsPort))))
}

func (modulePluginCatalog) ListPersonalPlugins(_ context.Context, subject string) ([]domain.PersonalPlugin, error) {
	if subject == "" {
		return nil, errors.New("fixture: personal selection requires a subject")
	}
	return nil, nil
}

func (modulePluginCatalog) ResolvePlugin(_ context.Context, subject, slug string) (domain.PluginRelease, error) {
	if subject == "" {
		return domain.PluginRelease{}, errors.New("fixture: release selection requires a subject")
	}
	roles := map[string][]string{"custom": {"custom:base", "custom:author"}, "talgent-runtime": {"talgent-runtime:personal-work-executor"}, "caller-plugin": {"caller-plugin:case-executor", "caller-plugin:coordinator", "caller-plugin:reviewer"}}
	names, ok := roles[slug]
	if !ok {
		return domain.PluginRelease{}, errors.New("fixture: unknown release")
	}
	release := domain.PluginRelease{Slug: slug, Source: "fixture", Version: "1", RetrievalRef: "fixture:" + slug, NativeEntry: []byte(fmt.Sprintf(`{"name":%q,"version":"1"}`, slug))}
	for _, name := range names {
		release.Agents = append(release.Agents, domain.AgentComponent{Name: name})
	}
	return release, nil
}

// Direct command fixtures supply available global capacity. Module tests use
// the production quota adapter with a controlled Kubernetes API.
type moduleAvailableCapacity struct{}

func (moduleAvailableCapacity) AdmitRoom(context.Context) error { return nil }

func (modulePluginCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
