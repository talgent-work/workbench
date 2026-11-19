package application

import (
	"context"
	"errors"
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
	// The authorization selections connector tests do not exercise stay
	// unimplemented.
	query.ProviderAuthorizationQueryRepository
	catalog        map[string]*domain.Connector
	installed      map[string]*domain.InstalledConnector
	authorizations []query.PersonalAuthorization
}

func newConnectorStore() *connectorStore {
	return &connectorStore{catalog: map[string]*domain.Connector{}, installed: map[string]*domain.InstalledConnector{}}
}

func (s *connectorStore) Get(_ context.Context, id string) (*domain.Connector, error) {
	connector, found := s.catalog[id]
	if !found {
		return nil, domain.ErrConnectorNotFound
	}
	copied := *connector
	return &copied, nil
}

func (s *connectorStore) GetPlatform(ctx context.Context, name string) (*domain.Connector, error) {
	return s.Get(ctx, s.idOf(name))
}

func (s *connectorStore) idOf(name string) string {
	for id, connector := range s.catalog {
		if connector.Name == name {
			return id
		}
	}
	return ""
}

func (s *connectorStore) Save(_ context.Context, connector *domain.Connector) error {
	if current, found := s.catalog[connector.ID]; found && current.Version != connector.Version {
		return domain.ErrConcurrentModification
	}
	copied := *connector
	copied.Version++
	s.catalog[connector.ID] = &copied
	return nil
}

func (s *connectorStore) Delete(_ context.Context, connector *domain.Connector) error {
	delete(s.catalog, connector.ID)
	return nil
}

func (s *connectorStore) ListPublished(ctx context.Context) ([]query.CatalogConnector, error) {
	all, err := s.ListForOwner(ctx, "")
	return slices.DeleteFunc(all, func(c query.CatalogConnector) bool { return c.State != query.ConnectorPublished }), err
}

func (s *connectorStore) ListForOwner(context.Context, string) ([]query.CatalogConnector, error) {
	states := map[domain.ConnectorState]string{domain.ConnectorPrivate: query.ConnectorPrivate, domain.ConnectorPublished: query.ConnectorPublished, domain.ConnectorWithdrawn: query.ConnectorWithdrawn}
	var result []query.CatalogConnector
	for _, c := range s.catalog {
		item := query.CatalogConnector{ID: c.ID, Name: c.Name, Description: c.Presentation.Description, Category: c.Presentation.Category, Required: c.Required,
			Owner: ownerOf(c.Owner), State: states[c.State], Authentication: c.Authentication, Version: c.Version,
			Configuration: query.ConnectorConfiguration{Type: c.Configuration.Type, URL: c.Configuration.URL, Command: c.Configuration.Command, Arguments: c.Configuration.Arguments}}
		if b := c.ProviderAccessBinding; b != nil {
			item.Provider = &query.ConnectorProvider{Provider: b.Provider, Target: b.Target, Authentication: b.Authentication}
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b query.CatalogConnector) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func ownerOf(owner domain.ConnectorOwner) query.ConnectorOwner {
	kinds := map[domain.OwnerKind]string{domain.OwnerPlatform: query.OwnerPlatform, domain.OwnerPerson: query.OwnerPerson, domain.OwnerApplication: query.OwnerApplication}
	return query.ConnectorOwner{Kind: kinds[owner.Kind], ID: owner.ID}
}

func (s *connectorStore) ListInstalled(_ context.Context, user string) ([]query.InstalledConnector, error) {
	var result []query.InstalledConnector
	for _, installation := range s.installed {
		if installation.OwnerID == user {
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
	key := installation.OwnerID + "/" + installation.ConnectorID
	if current, found := s.installed[key]; (found && current.Version != installation.Version) || (!found && installation.Version != 0) {
		return domain.ErrConcurrentModification
	}
	copied := *installation
	copied.Version++
	s.installed[key] = &copied
	return nil
}

func (s installedConnectors) Delete(_ context.Context, installation *domain.InstalledConnector) error {
	delete(s.installed, installation.OwnerID+"/"+installation.ConnectorID)
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
func (mcpCatalog) Name() string                                { return "talgent" }
func (mcpCatalog) OpenArchive(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, ErrArchiveNotFound
}

func connectorApplication(t *testing.T, plugins ...PluginRelease) (*Application, *connectorStore) {
	t.Helper()
	store := newConnectorStore()
	connections := NewPersonalConnections(ConnectionDependencies{Providers: staticProviders{gitHubDeclaration()}})
	app := NewApplication(nil, nil, mcpCatalog(plugins), store, connections, ConnectorDependencies{Catalog: store, Installed: installedConnectors{store}, CatalogQueries: store, InstalledQueries: store})
	require.NoError(t, app.EstablishPlatformConnector(t.Context(), PlatformConnector{Name: "talgent", Configuration: domain.MCPConfiguration{Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp"}}))
	configuration := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}}
	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	require.NoError(t, app.EstablishPlatformConnector(t.Context(), PlatformConnector{Name: "github", Configuration: configuration, Provider: &binding,
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
	app, store := connectorApplication(t)
	listed, err := app.ListConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Equal(t, ConnectorAlwaysEnabled, connectorNamed(t, listed, "talgent").Status)
	github := connectorNamed(t, listed, "github")
	require.Equal(t, ConnectorNotInstalled, github.Status)
	require.Equal(t, ProviderNotConnected, github.ProviderConnection)

	installed, err := app.InstallConnector(t.Context(), "user", store.idOf("github"))
	require.NoError(t, err, "a missing Provider authorization never blocks installation")
	require.Equal(t, ConnectorEnabled, installed.Status)
	require.Equal(t, ProviderNotConnected, installed.ProviderConnection)
	repeated, err := app.InstallConnector(t.Context(), "user", store.idOf("github"))
	require.NoError(t, err)
	require.Equal(t, installed.InstallationVersion, repeated.InstallationVersion, "repeating installation keeps the existing record")

	other, err := app.ListConnectors(t.Context(), "another-user")
	require.NoError(t, err)
	require.Equal(t, ConnectorNotInstalled, connectorNamed(t, other, "github").Status, "installations are personal")
}

func TestRequiredAndWithdrawnConnectorsRefuseInstallation(t *testing.T) {
	app, store := connectorApplication(t)
	_, err := app.InstallConnector(t.Context(), "user", store.idOf("talgent"))
	require.ErrorIs(t, err, domain.ErrConnectorNotInstallable)
	store.catalog[store.idOf("github")].State = domain.ConnectorWithdrawn
	_, err = app.InstallConnector(t.Context(), "user", store.idOf("github"))
	require.ErrorIs(t, err, domain.ErrConnectorNotFound, "a withdrawn Connector is in no catalog")
	listed, err := app.ListConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, listed, 1, "withdrawn entries are not browsable")
	selection, err := app.ReadConnectorSelection(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, selection.Catalog, 1, "a withdrawn entry the owner never installed is not selectable")
	require.True(t, selection.Catalog[0].Offered)
	require.NotEmpty(t, selection.Catalog[0].Configuration.URL, "the platform Connector reaches the Gateway")
	_, err = app.GetConnector(t.Context(), "user", store.idOf("github"))
	require.ErrorIs(t, err, domain.ErrConnectorNotFound)
}

func TestConnectorEnablementAndUninstallation(t *testing.T) {
	app, store := connectorApplication(t)
	installed, err := app.InstallConnector(t.Context(), "user", store.idOf("github"))
	require.NoError(t, err)
	_, err = app.DisableConnector(t.Context(), ConnectorInstallationCommand{OwnerID: "user", ConnectorID: store.idOf("github"), Version: installed.InstallationVersion + 1})
	require.ErrorIs(t, err, domain.ErrConcurrentModification, "a stale version is rejected")
	disabled, err := app.DisableConnector(t.Context(), ConnectorInstallationCommand{OwnerID: "user", ConnectorID: store.idOf("github"), Version: installed.InstallationVersion})
	require.NoError(t, err)
	require.Equal(t, ConnectorDisabled, disabled.Status)
	selection, err := app.ReadConnectorSelection(t.Context(), "user")
	require.NoError(t, err)
	require.Empty(t, selection.EnabledConnectorIDs, "disabled installations leave the default selection")
	enabled, err := app.EnableConnector(t.Context(), ConnectorInstallationCommand{OwnerID: "user", ConnectorID: store.idOf("github"), Version: disabled.InstallationVersion})
	require.NoError(t, err)
	require.Equal(t, ConnectorEnabled, enabled.Status)
	selection, err = app.ReadConnectorSelection(t.Context(), "user")
	require.NoError(t, err)
	require.Equal(t, []string{store.catalog[store.idOf("github")].ID}, selection.EnabledConnectorIDs)
	require.Len(t, selection.Catalog, 2)

	installedList, err := app.ListInstalledConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, installedList, 2, "required entries appear with the user's installations")
	require.NoError(t, app.UninstallConnector(t.Context(), ConnectorInstallationCommand{OwnerID: "user", ConnectorID: store.idOf("github"), Version: enabled.InstallationVersion}))
	require.NoError(t, app.UninstallConnector(t.Context(), ConnectorInstallationCommand{OwnerID: "user", ConnectorID: store.idOf("github")}), "uninstalling an absent installation succeeds")
	installedList, err = app.ListInstalledConnectors(t.Context(), "user")
	require.NoError(t, err)
	require.Len(t, installedList, 1)
}

func TestConnectorReportsCurrentProviderConnection(t *testing.T) {
	app, store := connectorApplication(t)
	for status, want := range map[string]string{"active": ProviderConnected, "reconnect_required": ProviderReconnectRequired, "revoked": ProviderNotConnected} {
		store.authorizations = []query.PersonalAuthorization{{ID: "authorization", Provider: "github", Status: status}}
		detail, err := app.GetConnector(t.Context(), "user", store.idOf("github"))
		require.NoError(t, err)
		require.Equal(t, "github", detail.Provider)
		require.Equal(t, want, detail.ProviderConnection, status)
	}
}

func TestPluginsRelateToConnectorsOnlyByEndpointEquivalence(t *testing.T) {
	uses := PluginRelease{Slug: "github-tools", Description: "Uses GitHub", MCPServers: []MCPComponent{{Name: "github", Definition: []byte(`{"command":"github-mcp-server","args":["stdio"]}`)}}}
	other := PluginRelease{Slug: "remote", MCPServers: []MCPComponent{{Name: "github", Definition: []byte(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)}}}
	app, store := connectorApplication(t, uses, other)
	detail, err := app.GetConnector(t.Context(), "user", store.idOf("github"))
	require.NoError(t, err)
	require.Equal(t, []ConnectorPlugin{{Slug: "github-tools", Description: "Uses GitHub"}}, detail.Plugins, "a same-named server at another endpoint is unrelated")

	releases := []PluginRelease{uses, other}
	require.NoError(t, app.relateConnectors(t.Context(), releases))
	require.Equal(t, "github", releases[0].MCPServers[0].Relationship.ConnectorName)
	require.Equal(t, "supported", releases[0].MCPServers[0].Relationship.Status)
	require.Equal(t, "github-api", releases[0].MCPServers[0].Relationship.Target)
	require.Equal(t, "relationship_not_registered", releases[1].MCPServers[0].Relationship.Reason, "a declaration without a catalog Connector is reported, not rejected")
}

type unreachableDirectory struct{}

func (unreachableDirectory) PersonNames(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("account unavailable")
}

func (unreachableDirectory) ApplicationName(context.Context, string) (string, error) {
	return "", errors.New("account unavailable")
}

// A publisher's name only presents a Connector: when Account cannot name it,
// the catalog and a Work's selection still read, naming it unknown.
func TestUnnamedPublishersNeverBlockTheCatalogOrSelection(t *testing.T) {
	app, _ := connectorApplication(t)
	app.publishers = unreachableDirectory{}
	added, err := app.AddConnector(t.Context(), ConnectorAuthoring{Owner: domain.ConnectorOwner{Kind: domain.OwnerPerson, ID: "alice"}, Name: "docs",
		Configuration: domain.MCPConfiguration{Type: "http", URL: "https://docs.example.com/mcp"}})
	require.NoError(t, err)
	_, err = app.PublishConnector(t.Context(), ConnectorCommand{OwnerID: "alice", ConnectorID: added.Connector.ID, Version: added.Connector.Version})
	require.NoError(t, err)

	listed, err := app.ListConnectors(t.Context(), "bob")
	require.NoError(t, err)
	require.Equal(t, "Unknown", connectorNamed(t, listed, "docs").Publisher.DisplayName)
	require.Empty(t, connectorNamed(t, listed, "github").Publisher.DisplayName, "the platform needs no name")
	selection, err := app.ReadConnectorSelection(t.Context(), "bob")
	require.NoError(t, err)
	index := slices.IndexFunc(selection.Catalog, func(c SelectableConnector) bool { return c.Name == "docs" })
	require.Equal(t, "Unknown", selection.Catalog[index].Publisher)
}
