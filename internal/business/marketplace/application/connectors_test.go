package application

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// connectorStore is one in-memory fake behind the Connector repositories and
// query contract, so writes are visible to the reads used by the use cases.
type connectorStore struct {
	catalog        map[string]*domain.Connector
	installed      map[string]*domain.InstalledConnector
	authorizations []query.PersonalAuthorization
}

func newConnectorStore() *connectorStore {
	return &connectorStore{catalog: map[string]*domain.Connector{}, installed: map[string]*domain.InstalledConnector{}}
}

func (s *connectorStore) GetByName(_ context.Context, name string) (*domain.Connector, error) {
	connector, found := s.catalog[name]
	if !found {
		return nil, domain.ErrConnectorNotFound
	}
	copied := *connector
	return &copied, nil
}

func (s *connectorStore) List(context.Context) ([]*domain.Connector, error) {
	var result []*domain.Connector
	for _, connector := range s.catalog {
		copied := *connector
		result = append(result, &copied)
	}
	slices.SortFunc(result, func(a, b *domain.Connector) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func (s *connectorStore) Save(_ context.Context, connector *domain.Connector) error {
	if current, found := s.catalog[connector.Name]; found && current.Version != connector.Version {
		return domain.ErrConcurrentModification
	}
	copied := *connector
	copied.Version++
	s.catalog[connector.Name] = &copied
	return nil
}

func (s *connectorStore) ListCatalog(context.Context) ([]query.CatalogConnector, error) {
	var result []query.CatalogConnector
	for _, c := range s.catalog {
		item := query.CatalogConnector{ID: c.ID, Name: c.Name, Description: c.Presentation.Description, Category: c.Presentation.Category, Required: c.Required,
			Offered: c.State == domain.ConnectorOffered, Authentication: c.Authentication,
			Configuration: query.ConnectorConfiguration{Type: c.Configuration.Type, URL: c.Configuration.URL, Command: c.Configuration.Command, Arguments: c.Configuration.Arguments}}
		if b := c.ProviderAccessBinding; b != nil {
			item.Provider = &query.ConnectorProvider{Provider: b.Provider, Target: b.Target, Authentication: b.Authentication}
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b query.CatalogConnector) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func (s *connectorStore) ListInstalled(_ context.Context, user string) ([]query.InstalledConnector, error) {
	var result []query.InstalledConnector
	for _, installation := range s.installed {
		if installation.UserID == user {
			result = append(result, query.InstalledConnector{ConnectorID: installation.ConnectorID, Enabled: installation.State == domain.Enabled, Version: installation.Version})
		}
	}
	return result, nil
}

func (s *connectorStore) ListPersonalAuthorizations(context.Context, string) ([]query.PersonalAuthorization, error) {
	return s.authorizations, nil
}

type installedConnectors struct{ *connectorStore }

func (s installedConnectors) Get(_ context.Context, user, connectorID string) (*domain.InstalledConnector, error) {
	installation, found := s.installed[user+"/"+connectorID]
	if !found {
		return nil, domain.ErrConnectorInstallationNotFound
	}
	copied := *installation
	return &copied, nil
}

func (s installedConnectors) Save(_ context.Context, installation *domain.InstalledConnector) error {
	key := installation.UserID + "/" + installation.ConnectorID
	if current, found := s.installed[key]; (found && current.Version != installation.Version) || (!found && installation.Version != 0) {
		return domain.ErrConcurrentModification
	}
	copied := *installation
	copied.Version++
	s.installed[key] = &copied
	return nil
}

func (s installedConnectors) Delete(_ context.Context, installation *domain.InstalledConnector) error {
	delete(s.installed, installation.UserID+"/"+installation.ConnectorID)
	return nil
}

// mcpCatalog holds Plugins whose MCP declarations may address catalog entries.
type mcpCatalog []PluginRelease

func (c mcpCatalog) Get(context.Context, string) (PluginRelease, error) {
	return PluginRelease{}, ErrReleaseNotFound
}
func (c mcpCatalog) List(context.Context, string, int, string) (ReleasePage, error) {
	return ReleasePage{Releases: c}, nil
}
func (mcpCatalog) Marketplace(context.Context) ([]byte, error) { return nil, nil }
func (mcpCatalog) OpenArchive(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, ErrArchiveNotFound
}

func connectorApplication(t *testing.T, plugins ...PluginRelease) (*Application, *connectorStore) {
	t.Helper()
	store := newConnectorStore()
	app := NewApplication(nil, nil, mcpCatalog(plugins), store, nil, ConnectorRepositories{Catalog: store, Installed: installedConnectors{store}, Queries: store})
	require.NoError(t, app.PublishConnector(t.Context(), ConnectorPublication{Name: "talgent", Configuration: domain.MCPConfiguration{Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp"}}))
	configuration := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}}
	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	evidence := domain.ProviderCompatibility{Configuration: configuration, Binding: binding}
	require.NoError(t, app.PublishConnector(t.Context(), ConnectorPublication{Name: "github", Configuration: configuration, Provider: &binding, Evidence: &evidence,
		Presentation: domain.CatalogPresentation{Description: "GitHub repositories and pull requests", Category: "development"}}))
	return app, store
}

func connectorNamed(t *testing.T, connectors []Connector, name string) Connector {
	t.Helper()
	index := slices.IndexFunc(connectors, func(c Connector) bool { return c.Name == name })
	require.GreaterOrEqual(t, index, 0, name)
	return connectors[index]
}

func TestConnectorInstallationDoesNotRequireProviderAuthorization(t *testing.T) {
	app, _ := connectorApplication(t)
	listed, err := app.ListConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Equal(t, ConnectorAlwaysEnabled, connectorNamed(t, listed, "talgent").Status)
	github := connectorNamed(t, listed, "github")
	require.Equal(t, ConnectorNotInstalled, github.Status)
	require.Equal(t, ProviderNotConnected, github.ProviderConnection)

	installed, err := app.InstallConnector(t.Context(), "user", "github")
	require.NoError(t, err, "a missing Provider authorization never blocks installation")
	require.Equal(t, ConnectorEnabled, installed.Status)
	require.Equal(t, ProviderNotConnected, installed.ProviderConnection)
	repeated, err := app.InstallConnector(t.Context(), "user", "github")
	require.NoError(t, err)
	require.Equal(t, installed.InstallationVersion, repeated.InstallationVersion, "repeating installation keeps the existing record")

	other, err := app.ListConnectors(t.Context(), "another-user")
	require.NoError(t, err)
	require.Equal(t, ConnectorNotInstalled, connectorNamed(t, other, "github").Status, "installations are personal")
}

func TestRequiredAndWithdrawnConnectorsRefuseInstallation(t *testing.T) {
	app, store := connectorApplication(t)
	_, err := app.InstallConnector(t.Context(), "user", "talgent")
	require.ErrorIs(t, err, domain.ErrConnectorNotInstallable)
	store.catalog["github"].State = domain.ConnectorWithdrawn
	_, err = app.InstallConnector(t.Context(), "user", "github")
	require.ErrorIs(t, err, domain.ErrConnectorNotInstallable)
	listed, err := app.ListConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, listed, 1, "withdrawn entries are not browsable")
	selection, err := app.ReadConnectorSelection(t.Context(), "user")
	require.NoError(t, err)
	github := selection.Catalog[0]
	require.Equal(t, "github", github.Name)
	require.False(t, github.Offered, "a withdrawn entry provides no connection")
	require.Empty(t, github.Configuration.Command)
	require.Nil(t, github.Provider)
	require.True(t, selection.Catalog[1].Offered)
	require.NotEmpty(t, selection.Catalog[1].Configuration.URL, "the platform Connector reaches the Gateway")
	_, err = app.GetConnector(t.Context(), "user", "github")
	require.ErrorIs(t, err, domain.ErrConnectorNotFound)
}

func TestConnectorEnablementAndUninstallation(t *testing.T) {
	app, store := connectorApplication(t)
	installed, err := app.InstallConnector(t.Context(), "user", "github")
	require.NoError(t, err)
	_, err = app.DisableConnector(t.Context(), ConnectorInstallationCommand{ActorUserID: "user", Name: "github", Version: installed.InstallationVersion + 1})
	require.ErrorIs(t, err, domain.ErrConcurrentModification, "a stale version is rejected")
	disabled, err := app.DisableConnector(t.Context(), ConnectorInstallationCommand{ActorUserID: "user", Name: "github", Version: installed.InstallationVersion})
	require.NoError(t, err)
	require.Equal(t, ConnectorDisabled, disabled.Status)
	selection, err := app.ReadConnectorSelection(t.Context(), "user")
	require.NoError(t, err)
	require.Empty(t, selection.EnabledConnectorIDs, "disabled installations leave the default selection")
	enabled, err := app.EnableConnector(t.Context(), ConnectorInstallationCommand{ActorUserID: "user", Name: "github", Version: disabled.InstallationVersion})
	require.NoError(t, err)
	require.Equal(t, ConnectorEnabled, enabled.Status)
	selection, err = app.ReadConnectorSelection(t.Context(), "user")
	require.NoError(t, err)
	require.Equal(t, []string{store.catalog["github"].ID}, selection.EnabledConnectorIDs)
	require.Len(t, selection.Catalog, 2)

	installedList, err := app.ListInstalledConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, installedList, 2, "required entries appear with the user's installations")
	require.NoError(t, app.UninstallConnector(t.Context(), ConnectorInstallationCommand{ActorUserID: "user", Name: "github", Version: enabled.InstallationVersion}))
	require.NoError(t, app.UninstallConnector(t.Context(), ConnectorInstallationCommand{ActorUserID: "user", Name: "github"}), "uninstalling an absent installation succeeds")
	installedList, err = app.ListInstalledConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, installedList, 1)
}

func TestConnectorReportsCurrentProviderConnection(t *testing.T) {
	app, store := connectorApplication(t)
	for status, want := range map[string]string{"active": ProviderConnected, "reconnect_required": ProviderReconnectRequired, "revoked": ProviderNotConnected} {
		store.authorizations = []query.PersonalAuthorization{{ID: "authorization", Provider: "github", Status: status}}
		detail, err := app.GetConnector(t.Context(), "user", "github")
		require.NoError(t, err)
		require.Equal(t, "github", detail.Provider)
		require.Equal(t, want, detail.ProviderConnection, status)
	}
}

func TestPluginsRelateToConnectorsOnlyByEndpointEquivalence(t *testing.T) {
	uses := PluginRelease{Slug: "github-tools", Description: "Uses GitHub", MCPServers: []MCPComponent{{Name: "github", Definition: []byte(`{"command":"github-mcp-server","args":["stdio"]}`)}}}
	other := PluginRelease{Slug: "remote", MCPServers: []MCPComponent{{Name: "github", Definition: []byte(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)}}}
	app, _ := connectorApplication(t, uses, other)
	detail, err := app.GetConnector(t.Context(), "user", "github")
	require.NoError(t, err)
	require.Equal(t, []ConnectorPlugin{{Slug: "github-tools", Description: "Uses GitHub"}}, detail.Plugins, "a same-named server at another endpoint is unrelated")

	releases := []PluginRelease{uses, other}
	require.NoError(t, app.relateConnectors(t.Context(), releases))
	require.Equal(t, "github", releases[0].MCPServers[0].Relationship.ConnectorName)
	require.Equal(t, "supported", releases[0].MCPServers[0].Relationship.Status)
	require.Equal(t, "github-api", releases[0].MCPServers[0].Relationship.Target)
	require.Equal(t, "relationship_not_registered", releases[1].MCPServers[0].Relationship.Reason, "a declaration without a catalog Connector is reported, not rejected")
}
