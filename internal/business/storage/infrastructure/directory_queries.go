package infrastructure

import (
	"context"
	"strings"

	"github.com/samber/oops"
	"xorm.io/builder"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/storage/application/query"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// DirectoryQueries reads Directory facts from the Directory table.
type DirectoryQueries struct {
	engine *xorm.Engine
}

var _ query.DirectoryQueryRepository = (*DirectoryQueries)(nil)

func NewDirectoryQueries(engine *xorm.Engine) *DirectoryQueries {
	return &DirectoryQueries{engine: engine}
}

// WorkDirectories reads the Directories with the Work's lifetime whose owner
// names the Work, by the indexed Work fact.
func (r *DirectoryQueries) WorkDirectories(ctx context.Context, workID string) ([]query.WorkDirectory, error) {
	var rows []directoryDO
	err := r.engine.Context(ctx).Cols("owner_key", "name", "backend", "lifetime", "state").
		Where("work_id = ? AND lifetime = ? AND deleted_at = 0", workID, string(domain.LifetimeWork)).Asc("owner_key", "name").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "storage.directories.of_work").Wrap(err)
	}
	directories := make([]query.WorkDirectory, 0, len(rows))
	for _, row := range rows {
		id, err := directoryIDFromDO(row.OwnerKey, row.Name)
		if err != nil {
			return nil, err
		}
		directories = append(directories, query.WorkDirectory{
			ID: id, Backend: domain.Backend(row.Backend), Lifetime: domain.Lifetime(row.Lifetime), State: domain.DirectoryState(row.State),
		})
	}
	return directories, nil
}

// MappingsAround reads the mapping at the path, those above it by their
// exact ancestor paths, and those inside it by the key range of paths
// beginning with "<path>/": "/" sorts right before "0" in utf8mb4_bin.
func (r *DirectoryQueries) MappingsAround(ctx context.Context, path domain.MappedPath) ([]query.Mapping, error) {
	around := []any{string(path)}
	for ancestor := string(path); strings.Contains(ancestor, "/"); {
		ancestor = ancestor[:strings.LastIndex(ancestor, "/")]
		around = append(around, ancestor)
	}
	var rows []mappingDO
	err := r.engine.Context(ctx).Cols(mappingColumns...).
		Where(builder.Or(builder.In("path", around...), builder.And(builder.Gt{"path": string(path) + "/"}, builder.Lt{"path": string(path) + "0"}))).
		Asc("path").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "storage.mappings.around").Wrap(err)
	}
	return mappingsFromDO(rows)
}

// Mappings reads every mapping by path.
func (r *DirectoryQueries) Mappings(ctx context.Context) ([]query.Mapping, error) {
	var rows []mappingDO
	if err := r.engine.Context(ctx).Cols(mappingColumns...).Asc("path").Find(&rows); err != nil {
		return nil, oops.With("operation", "storage.mappings.all").Wrap(err)
	}
	return mappingsFromDO(rows)
}

func mappingsFromDO(rows []mappingDO) ([]query.Mapping, error) {
	mappings := make([]query.Mapping, 0, len(rows))
	for _, row := range rows {
		id, err := directoryIDFromDO(row.OwnerKey, row.DirectoryName)
		if err != nil {
			return nil, err
		}
		path, err := domain.ParseMappedPath(row.Path)
		if err != nil {
			return nil, oops.With("mapped_path", row.Path).Wrap(err)
		}
		mappings = append(mappings, query.Mapping{Path: path, Directory: id})
	}
	return mappings, nil
}
