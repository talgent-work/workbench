package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// retirementBatch bounds each read and soft delete of a removed source.
const retirementBatch = 100

type retiringReleaseDO struct {
	ID       string `xorm:"id pk"`
	Source   string `xorm:"source"`
	Slug     string `xorm:"slug"`
	Version  string `xorm:"release_version"`
	Metadata string `xorm:"metadata"`
}

func (retiringReleaseDO) TableName() string { return "marketplace_plugin_release" }

// RetireSources runs under the catalog lock right after a commit, so the
// visible generation already lists only configured sources. A Work keeps the
// native entry of each release it took up, but each start, Resume or Restart
// takes up the current release by slug from the catalog; a release of a
// removed source is no longer offered, and a Work that still names it fails to
// install that one Plugin while the rest of its start proceeds. Archives are
// content-addressed, so one any live release of a configured source
// references stays. Each batch removes its archives before its rows are
// soft-deleted: an interrupted retirement leaves rows the next one finds.
func (s *CatalogStore) RetireSources(ctx context.Context, configured []string) (application.SourceRetirement, error) {
	var result application.SourceRetirement
	if len(configured) == 0 {
		return result, errors.New("marketplace: retirement requires the configured sources")
	}
	referenced, err := s.referencedArchives(ctx, configured)
	if err != nil {
		return result, err
	}
	now := time.Now().UnixMilli()
	for {
		statement := []any{`UPDATE marketplace_catalog_entry SET deleted_at=?,updated_at=?,version=version+1 WHERE deleted_at=0 AND source NOT IN (` + placeholders(len(configured)) + `) LIMIT 500`, now, now}
		for _, name := range configured {
			statement = append(statement, name)
		}
		affected, err := s.engine.Context(ctx).Exec(statement...)
		if err != nil {
			return result, oops.With("operation", "marketplace.source.retire_entries").Wrap(err)
		}
		if count, _ := affected.RowsAffected(); count < 500 {
			break
		}
	}
	removed := map[string]bool{}
	for {
		var rows []retiringReleaseDO
		if err := s.engine.Context(ctx).Where("deleted_at = 0").NotIn("source", configured).Asc("id").Limit(retirementBatch).Find(&rows); err != nil {
			return result, oops.With("operation", "marketplace.source.read_retiring").Wrap(err)
		}
		if len(rows) == 0 {
			return result, nil
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			if digest := releaseArchive(row.Metadata); digest != "" && !referenced[digest] && !removed[digest] {
				deleted, err := s.removeArchive(digest)
				if err != nil {
					return result, err
				}
				removed[digest] = true
				if deleted {
					result.Archives = append(result.Archives, digest)
				}
			}
			ids = append(ids, row.ID)
		}
		affected, err := s.engine.Context(ctx).Table("marketplace_plugin_release").Where("deleted_at = 0").In("id", ids).Incr("version").Update(map[string]any{"deleted_at": now, "updated_at": now})
		if err != nil {
			return result, oops.With("operation", "marketplace.source.retire_releases").Wrap(err)
		}
		// The catalog lock excludes another publication; no progress means a
		// writer outside it, which must not turn this into an endless loop.
		if affected == 0 {
			return result, errors.New("marketplace: retiring releases made no progress")
		}
		for _, row := range rows {
			result.Releases = append(result.Releases, application.RetiredRelease{Source: row.Source, Slug: row.Slug, Version: row.Version})
		}
		if len(rows) < retirementBatch {
			return result, nil
		}
	}
}

// referencedArchives are the archive digests of every live release of a
// configured source, current or retained, read in bounded pages.
func (s *CatalogStore) referencedArchives(ctx context.Context, configured []string) (map[string]bool, error) {
	referenced := map[string]bool{}
	cursor := ""
	for {
		var rows []retiringReleaseDO
		if err := s.engine.Context(ctx).Cols("id", "metadata").Where("deleted_at = 0 AND id > ?", cursor).In("source", configured).Asc("id").Limit(500).Find(&rows); err != nil {
			return nil, oops.With("operation", "marketplace.source.read_referenced").Wrap(err)
		}
		for _, row := range rows {
			if digest := releaseArchive(row.Metadata); digest != "" {
				referenced[digest] = true
			}
			cursor = row.ID
		}
		if len(rows) < 500 {
			return referenced, nil
		}
	}
}

// releaseArchive is the lowercase digest a release's archive source pins, or
// empty for any other source.
func releaseArchive(metadata string) string {
	release, err := decodeRelease(metadata)
	if err != nil {
		return ""
	}
	var entry struct {
		Source struct {
			Kind   string `json:"source"`
			Digest string `json:"sha256"`
		} `json:"source"`
	}
	if json.Unmarshal(release.NativeEntry, &entry) != nil || entry.Source.Kind != "archive" || !archiveDigest.MatchString(entry.Source.Digest) {
		return ""
	}
	return strings.ToLower(entry.Source.Digest)
}

// removeArchive reports whether the content store held the archive. Only
// owned packages are stored; a third-party archive was never there.
func (s *CatalogStore) removeArchive(digest string) (bool, error) {
	root, err := os.OpenRoot(filepath.Join(s.directory, "archives"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	err = root.Remove(digest + ".zip")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
