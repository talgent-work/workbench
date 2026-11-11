package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"xorm.io/xorm"
)

type SourceReleases = application.SourceReleases

// CatalogStore queries one release or page from MySQL. The content directory
// contains archives and synchronization scratch files, never query metadata.
type CatalogStore struct {
	engine                 *xorm.Engine
	directory, name, owner string
	logger                 *slog.Logger
}

var _ application.CatalogPort = (*CatalogStore)(nil)
var _ application.CatalogPublicationPort = (*CatalogStore)(nil)

func NewCatalogStore(engine *xorm.Engine, directory, name, owner string, logger *slog.Logger) (*CatalogStore, error) {
	if engine == nil || !filepath.IsAbs(directory) || name == "" || owner == "" || logger == nil {
		return nil, errors.New("marketplace: database, catalog path, name, owner and logger are required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	return &CatalogStore{engine: engine, directory: directory, name: name, owner: owner, logger: logger}, nil
}
func (s *CatalogStore) Publish(ctx context.Context, sources []SourceReleases) error {
	lock := flock.New(filepath.Join(s.directory, "catalog.lock"))
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return ctx.Err()
	}
	defer lock.Unlock()
	return application.PublishCatalog(ctx, s, sources, s.logger)
}
func (s *CatalogStore) PublishedRelease(ctx context.Context, source, slug, version string) (*application.PluginRelease, error) {
	var row catalogReleaseDO
	found, err := s.engine.Context(ctx).Cols("metadata").Where("source = ? AND slug = ? AND release_version = ?", source, slug, version).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "marketplace.release.read").Wrap(err)
	}
	if !found {
		return nil, nil
	}
	value, err := decodeRelease(row.Metadata)
	return &value, err
}
func (s *CatalogStore) RetainRelease(ctx context.Context, release application.PluginRelease) error {
	encoded, err := json.Marshal(release)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	// Publication identity remains source + slug + version. The UUID is only a
	// database locator; duplicate inserts must agree on the immutable content.
	// A release retired with its source comes back when the source does.
	_, err = s.engine.Context(ctx).Exec(`INSERT INTO marketplace_plugin_release (id,source,slug,release_version,metadata,version,created_at,updated_at,deleted_at) VALUES (?,?,?,?,?,1,?,?,0) ON DUPLICATE KEY UPDATE version=IF(deleted_at=0,version,version+1),updated_at=IF(deleted_at=0,updated_at,VALUES(updated_at)),deleted_at=0`, uuid.Must(uuid.NewV7()).String(), release.Source, release.Slug, release.Version, string(encoded), now, now)
	if err != nil {
		return oops.With("operation", "marketplace.release.retain").Wrap(err)
	}
	published, err := s.PublishedRelease(ctx, release.Source, release.Slug, release.Version)
	if err != nil {
		return err
	}
	if published == nil {
		return application.ErrReleaseNotFound
	}
	previous := application.AssemblePluginReleaseDTO(*published)
	return (domain.PluginReleasePolicy{}).AcceptRelease(application.AssemblePluginReleaseDTO(release), &previous)
}
func (s *CatalogStore) CurrentSource(ctx context.Context, source string) ([]application.PluginRelease, error) {
	var result []application.PluginRelease
	cursor := ""
	for {
		var rows []catalogReleaseDO
		err := s.current(ctx).Where("e.source = ? AND e.slug > ?", source, cursor).Asc("e.slug").Limit(100).Find(&rows)
		if err != nil {
			return nil, oops.With("operation", "marketplace.source.read").Wrap(err)
		}
		for _, row := range rows {
			value, err := decodeRelease(row.Metadata)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
			cursor = value.Slug
		}
		if len(rows) < 100 {
			return result, nil
		}
	}
}
func (s *CatalogStore) CommitCatalog(ctx context.Context, sources []SourceReleases) error {
	generation := uuid.Must(uuid.NewV7()).String()
	var candidates []domain.CatalogCandidate
	for _, source := range sources {
		for _, release := range source.Releases {
			candidates = append(candidates, domain.CatalogCandidate{Source: source.Source.Name, Slug: release.Slug, Priority: source.Source.Priority, Owned: source.Source.Owned})
		}
	}
	selected := (domain.PluginSourceSelection{}).SelectSource(candidates)
	index := 0
	now := time.Now().UnixMilli()
	rows := make([]catalogEntryDO, 0, len(candidates))
	for _, source := range sources {
		for _, release := range source.Releases {
			var stored catalogReleaseDO
			found, err := s.engine.Context(ctx).Cols("id").Where("source = ? AND slug = ? AND release_version = ?", release.Source, release.Slug, release.Version).Get(&stored)
			if err != nil {
				return err
			}
			if !found {
				return application.ErrReleaseNotFound
			}
			rows = append(rows, catalogEntryDO{ID: uuid.Must(uuid.NewV7()).String(), Generation: generation, Source: release.Source, Slug: release.Slug, ReleaseID: stored.ID, Selected: selected[release.Slug] == index, HasSkill: len(release.Skills) > 0, HasAgent: len(release.Agents) > 0, HasMCP: len(release.MCPServers) > 0, Version: 1, CreatedAt: now, UpdatedAt: now})
			index++
		}
	}
	// Stage bounded batches outside the visibility transaction. A crash leaves
	// unreachable rows, never a partial catalog. One pointer publishes all rows.
	for offset := 0; offset < len(rows); offset += 500 {
		end := min(offset+500, len(rows))
		if _, err := s.engine.Context(ctx).Insert(rows[offset:end]); err != nil {
			return oops.With("operation", "marketplace.catalog.stage").Wrap(err)
		}
	}
	_, err := s.engine.Context(ctx).Exec(`INSERT INTO marketplace_catalog_head (id,generation,version,created_at,updated_at,deleted_at) VALUES (?,?,1,?,?,0) ON DUPLICATE KEY UPDATE generation=VALUES(generation),version=version+1,updated_at=VALUES(updated_at)`, s.name, generation, now, now)
	if err != nil {
		return oops.With("operation", "marketplace.catalog.publish").Wrap(err)
	}
	return nil
}
func (s *CatalogStore) current(ctx context.Context) *xorm.Session {
	return s.engine.Context(ctx).Table("marketplace_catalog_entry").Alias("e").Join("INNER", "marketplace_catalog_head AS h", "h.generation = e.generation").Join("INNER", "marketplace_plugin_release AS r", "r.id = e.release_id").Cols("r.metadata").Where("h.id = ?", s.name)
}
func (s *CatalogStore) Get(ctx context.Context, slug string) (application.PluginRelease, error) {
	var row catalogReleaseDO
	found, err := s.current(ctx).Where("e.selected = 1 AND e.slug = ?", slug).Get(&row)
	if err != nil {
		return application.PluginRelease{}, oops.With("operation", "marketplace.catalog.get").Wrap(err)
	}
	if !found {
		return application.PluginRelease{}, application.ErrReleaseNotFound
	}
	return decodeRelease(row.Metadata)
}
func (s *CatalogStore) List(ctx context.Context, after string, limit int, component string) (application.ReleasePage, error) {
	q := s.current(ctx).Where("e.selected = 1 AND e.slug > ?", after).Asc("e.slug").Limit(limit + 1)
	switch component {
	case "skill":
		q.And("e.has_skill = 1")
	case "agent":
		q.And("e.has_agent = 1")
	case "mcp":
		q.And("e.has_mcp = 1")
	case "":
	default:
		q.Close()
		return application.ReleasePage{}, application.ErrInvalidQuery
	}
	var rows []catalogReleaseDO
	if err := q.Find(&rows); err != nil {
		return application.ReleasePage{}, oops.With("operation", "marketplace.catalog.list").Wrap(err)
	}
	page := application.ReleasePage{}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	for _, row := range rows {
		release, err := decodeRelease(row.Metadata)
		if err != nil {
			return application.ReleasePage{}, err
		}
		page.Releases = append(page.Releases, release)
	}
	if more {
		page.NextCursor = page.Releases[len(page.Releases)-1].Slug
	}
	return page, nil
}
func (s *CatalogStore) Marketplace(ctx context.Context) ([]byte, error) {
	var head struct {
		Generation string `xorm:"generation"`
	}
	found, err := s.engine.Context(ctx).Table("marketplace_catalog_head").Cols("generation").Where("id = ?", s.name).Get(&head)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, application.ErrCatalogNotReady
	}
	// A single SQL read observes one published generation, including during sync.
	var rows []catalogReleaseDO
	if err := s.current(ctx).Where("e.selected = 1").Asc("e.slug").Find(&rows); err != nil {
		return nil, err
	}
	entries := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		release, err := decodeRelease(row.Metadata)
		if err != nil {
			return nil, err
		}
		sensitive := false
		for _, declaration := range release.Parameters {
			sensitive = sensitive || declaration.Sensitive
		}
		entries = append(entries, json.RawMessage(pluginconfig.PublicNativeEntry(release.NativeEntry, sensitive)))
	}
	return json.Marshal(struct {
		Name    string            `json:"name"`
		Owner   map[string]string `json:"owner"`
		Plugins []json.RawMessage `json:"plugins"`
	}{s.name, map[string]string{"name": s.owner}, entries})
}
