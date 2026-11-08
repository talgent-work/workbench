package infrastructure

import (
	"context"

	"github.com/samber/oops"
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
