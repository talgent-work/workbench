package infrastructure

import (
	"context"
	"errors"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

const mysqlDuplicateEntry = 1062

var (
	fileSpaceColumns = []string{"owner", "space_token", "version"}
	shareLinkColumns = []string{"token", "owner", "target_kind", "target_name", "created_by", "state", "created_at"}
)

// FileSpaceRepository persists File Spaces with their Share Links; the space
// row's version serializes every change of one space.
type FileSpaceRepository struct {
	engine *xorm.Engine
}

var _ domain.FileSpaceRepository = (*FileSpaceRepository)(nil)

func NewFileSpaceRepository(engine *xorm.Engine) *FileSpaceRepository {
	return &FileSpaceRepository{engine: engine}
}

func (r *FileSpaceRepository) Get(ctx context.Context, owner string) (*domain.FileSpace, error) {
	return r.get(ctx, "owner = ?", owner, domain.ErrSpaceNotOpen)
}

func (r *FileSpaceRepository) GetBySpaceToken(ctx context.Context, token string) (*domain.FileSpace, error) {
	return r.get(ctx, "space_token = ?", token, domain.ErrSpaceTokenRejected)
}

func (r *FileSpaceRepository) get(ctx context.Context, condition string, value string, absent error) (*domain.FileSpace, error) {
	row := new(fileSpaceDO)
	found, err := r.engine.Context(ctx).Cols(fileSpaceColumns...).Where(condition+" AND deleted_at = 0", value).Get(row)
	if err != nil {
		return nil, oops.With("operation", "storage.file_space.get").Wrap(err)
	}
	if !found {
		return nil, absent
	}
	var links []shareLinkDO
	if err := r.engine.Context(ctx).Cols(shareLinkColumns...).Where("owner = ? AND deleted_at = 0", row.Owner).Asc("created_at", "token").Find(&links); err != nil {
		return nil, oops.With("operation", "storage.share_links.list", "owner", row.Owner).Wrap(err)
	}
	return fileSpaceFromDO(row, links), nil
}

func (r *FileSpaceRepository) GetByShareToken(ctx context.Context, token string) (*domain.FileSpace, error) {
	link := new(shareLinkDO)
	found, err := r.engine.Context(ctx).Cols("owner").Where("token = ? AND deleted_at = 0", token).Get(link)
	if err != nil {
		return nil, oops.With("operation", "storage.share_link.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrShareLinkNotFound
	}
	return r.Get(ctx, link.Owner)
}

func (r *FileSpaceRepository) Save(ctx context.Context, space *domain.FileSpace) error {
	session := r.engine.NewSession().Context(ctx)
	defer session.Close()
	if err := session.Begin(); err != nil {
		return oops.With("operation", "storage.file_space.begin").Wrap(err)
	}
	if err := saveFileSpace(session, space); err != nil {
		_ = session.Rollback()
		return err
	}
	if err := session.Commit(); err != nil {
		return oops.With("operation", "storage.file_space.commit", "owner", space.Owner).Wrap(err)
	}
	return nil
}

func saveFileSpace(tx *xorm.Session, space *domain.FileSpace) error {
	now := time.Now().UnixMilli()
	row := fileSpaceToDO(space)
	row.UpdatedAt = now
	if space.Version == 0 {
		row.Version, row.CreatedAt = 1, now
		if _, err := tx.Insert(row); err != nil {
			var duplicate *drivermysql.MySQLError
			if errors.As(err, &duplicate) && duplicate.Number == mysqlDuplicateEntry {
				return domain.ErrConcurrentModification
			}
			return oops.With("operation", "storage.file_space.insert", "owner", space.Owner).Wrap(err)
		}
	} else {
		affected, err := tx.Cols("updated_at").Incr("version").
			Where("owner = ? AND version = ? AND deleted_at = 0", space.Owner, space.Version).Update(row)
		if err != nil {
			return oops.With("operation", "storage.file_space.update", "owner", space.Owner).Wrap(err)
		}
		if affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	for _, link := range space.ShareLinks {
		data := shareLinkToDO(space.Owner, link)
		_, err := tx.Exec(
			"INSERT INTO storage_share_link (token, owner, target_kind, target_name, created_by, state, version, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, 0) "+
				// MySQL assigns left to right: version compares the stored values first.
				"ON DUPLICATE KEY UPDATE version = IF(target_kind <> VALUES(target_kind) OR target_name <> VALUES(target_name) OR state <> VALUES(state), version + 1, version), "+
				"target_kind = VALUES(target_kind), target_name = VALUES(target_name), state = VALUES(state), updated_at = VALUES(updated_at)",
			data.Token, data.Owner, data.TargetKind, data.TargetName, data.CreatedBy, data.State, data.CreatedAt, now)
		if err != nil {
			return oops.With("operation", "storage.share_link.save", "owner", space.Owner).Wrap(err)
		}
	}
	return nil
}
