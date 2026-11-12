package application

import (
	"context"
	"encoding/hex"
	"io"
)

// PluginRelease is immutable catalog content, not a mutable aggregate. The
// native entry and parsed declarations describe the same published package.
type PluginRelease struct {
	// CatalogEntryName locates an aliased upstream entry on a later failed refresh.
	// It is acquisition bookkeeping, never the published Plugin identity.
	CatalogEntryName string `json:",omitempty"`
	Slug             string
	Source           string
	Version          string
	Description      string
	RetrievalRef     string
	NativeEntry      []byte
	Skills           []SkillComponent
	Agents           []AgentComponent
	MCPServers       []MCPComponent
	Parameters       map[string]ParameterDeclaration
}

// Components are declarations only. Package file contents stay in the package
// archive and are never stored or published.
type SkillComponent struct {
	Name         string
	Description  string
	ArgumentHint string
}

type AgentComponent struct {
	Name        string
	Description string
}

type MCPComponent struct {
	Relationship *ConnectorRelationship
	Name         string
	Definition   []byte
}

type ParameterDeclaration struct {
	Type, Title, Description      string
	Required, Sensitive, Multiple bool
	Default                       any
	Options                       []string
	Min, Max                      *float64
}

type ReleasePage struct {
	Releases   []PluginRelease
	NextCursor string
}

// PluginCatalogQueryRepository reads the synchronized catalog. Implementations
// must not fetch or reinterpret mutable package contents during these reads.
type PluginCatalogQueryRepository interface {
	Get(ctx context.Context, slug string) (PluginRelease, error)
	List(ctx context.Context, afterSlug string, limit int, component string) (ReleasePage, error)
	Marketplace(ctx context.Context) ([]byte, error)
	OpenArchive(ctx context.Context, digest string) (io.ReadSeekCloser, error)
	// Name is the catalog's own name, which names it wherever it is
	// distributed.
	Name() string
}

// ResolvedRelease is a Plugin's current release as a Work's execution takes
// it, with the name of the catalog that publishes it: a Harness installs the
// release from a marketplace of that name.
type ResolvedRelease struct {
	Release     PluginRelease
	CatalogName string
}

// ResolveRelease resolves a Plugin's current release for Work execution.
func (a *Application) ResolveRelease(ctx context.Context, slug string) (ResolvedRelease, error) {
	release, err := a.GetRelease(ctx, slug)
	if err != nil {
		return ResolvedRelease{}, err
	}
	return ResolvedRelease{Release: release, CatalogName: a.catalog.Name()}, nil
}

func (a *Application) GetRelease(ctx context.Context, slug string) (PluginRelease, error) {
	if slug == "" {
		return PluginRelease{}, ErrInvalidQuery
	}
	release, err := a.catalog.Get(ctx, slug)
	if err != nil {
		return PluginRelease{}, err
	}
	releases := []PluginRelease{release}
	if err := a.relateConnectors(ctx, releases); err != nil {
		return PluginRelease{}, err
	}
	return releases[0], nil
}

// relateConnectors annotates MCP declarations with their corresponding catalog
// Connector, reading the catalog once for all supplied releases.
func (a *Application) relateConnectors(ctx context.Context, releases []PluginRelease) error {
	catalog, err := a.connectorQueries.ListCatalog(ctx)
	if err != nil {
		return err
	}
	index := indexConnectorEndpoints(catalog)
	for i := range releases {
		for j := range releases[i].MCPServers {
			relationship := index.relate(releases[i].MCPServers[j].Definition)
			releases[i].MCPServers[j].Relationship = &relationship
		}
	}
	return nil
}

func (a *Application) ListReleases(ctx context.Context, afterSlug string, limit int, component string) (ReleasePage, error) {
	limit, err := pageLimit(limit)
	if err != nil {
		return ReleasePage{}, err
	}
	if component != "" && component != "skill" && component != "agent" && component != "mcp" {
		return ReleasePage{}, ErrInvalidQuery
	}
	// A list is summarized, so MCP servers are related to Connectors only on
	// GetRelease, where their declarations are shown.
	return a.catalog.List(ctx, afterSlug, limit, component)
}

func (a *Application) Marketplace(ctx context.Context) ([]byte, error) {
	return a.catalog.Marketplace(ctx)
}

// OpenArchive reads only an immutable, platform-published package. It never
// proxies third-party content or exposes personal installation configuration.
func (a *Application) OpenArchive(ctx context.Context, digest string) (io.ReadSeekCloser, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return nil, ErrInvalidQuery
	}
	return a.catalog.OpenArchive(ctx, digest)
}

func pageLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidQuery
	}
	return limit, nil
}
