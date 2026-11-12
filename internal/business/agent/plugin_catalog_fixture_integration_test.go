//go:build integration

package agent_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"go.uber.org/fx"
)

// Lifecycle module tests control the external catalog, not Agent selection or
// snapshot assembly. Marketplace API/native catalog journeys test that boundary.
type modulePluginCatalog struct{}

func moduleCatalog() fx.Option {
	return fx.Replace(fx.Annotate(commandtest.StartCatalog(modulePluginCatalog{}, authorizationcatalogtest.Empty{}), fx.As(new(command.MarketplacePort))))
}

func (modulePluginCatalog) ListPersonalPlugins(_ context.Context, subject string) ([]domain.PersonalPlugin, error) {
	if subject == "" {
		return nil, errors.New("fixture: personal selection requires a subject")
	}
	if subject == supportDesk {
		return supportDeskPlugins, nil
	}
	return nil, nil
}

// supportDeskPlugins are the Support Desk Application's installations: one
// enabled, one disabled.
var supportDeskPlugins = []domain.PersonalPlugin{{Slug: "custom", Enabled: true}, {Slug: "caller-plugin", Enabled: false}}

// supportDeskConnector is the catalog Connector the Support Desk Application
// installed and enabled.
var supportDeskConnector = connectorcatalogtest.GitHub()

func (modulePluginCatalog) ResolvePlugin(_ context.Context, subject, slug string) (domain.PluginRelease, error) {
	if subject == "" {
		return domain.PluginRelease{}, errors.New("fixture: release selection requires a subject")
	}
	roles := map[string][]string{"custom": {"custom:base", "custom:author"}, "talgent": {"talgent:work-executor"}, "caller-plugin": {"caller-plugin:case-executor", "caller-plugin:coordinator", "caller-plugin:reviewer"}}
	names, ok := roles[slug]
	if !ok {
		return domain.PluginRelease{}, errors.New("fixture: unknown release")
	}
	release := domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: "fixture", Version: "1", RetrievalRef: "fixture:" + slug, NativeEntry: []byte(fmt.Sprintf(`{"name":%q,"version":"1"}`, slug))}
	for _, name := range names {
		release.Agents = append(release.Agents, domain.AgentComponent{Name: name})
	}
	return release, nil
}

// Direct command fixtures supply available global capacity. Module tests use
// the production quota adapter with a controlled Kubernetes API.
type moduleAvailableCapacity struct{}

func (moduleAvailableCapacity) AdmitRoom(context.Context) error { return nil }

func (modulePluginCatalog) ReadConnectorSelection(_ context.Context, subject string) (command.ConnectorSelection, error) {
	selection := connectorcatalogtest.Selection()
	selection.Catalog = append(selection.Catalog, supportDeskConnector)
	if subject == supportDesk {
		selection.EnabledConnectors = []string{supportDeskConnector.ID}
	}
	return selection, nil
}
