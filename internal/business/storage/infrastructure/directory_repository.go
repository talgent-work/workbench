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

const (
	mysqlDuplicateEntry = 1062
	// maxOwnerKeyBytes is what one path element holds: an owner is laid out
	// as one.
	maxOwnerKeyBytes = 255
)

var (
	directoryColumns = []string{"owner_key", "name", "backend", "lifetime", "state", "space_token", "version"}
	shareLinkColumns = []string{"token", "owner_key", "directory_name", "target_name", "created_by", "state", "created_at"}
)

// DirectoryRepository persists Directories with their Share Links; the
// Directory row's version serializes every change of one Directory.
type DirectoryRepository struct {
	engine *xorm.Engine
}

var _ domain.DirectoryRepository = (*DirectoryRepository)(nil)

func NewDirectoryRepository(engine *xorm.Engine) *DirectoryRepository {
	return &DirectoryRepository{engine: engine}
}

func (r *DirectoryRepository) Get(ctx context.Context, id domain.DirectoryID) (*domain.Directory, error) {
	return r.get(ctx, "owner_key = ? AND name = ?", []any{ownerKey(id.Owner), id.Name}, domain.ErrDirectoryNotOpen)
}

func (r *DirectoryRepository) GetBySpaceToken(ctx context.Context, token string) (*domain.Directory, error) {
	return r.get(ctx, "space_token = ?", []any{token}, domain.ErrSpaceTokenRejected)
}

func (r *DirectoryRepository) get(ctx context.Context, condition string, values []any, absent error) (*domain.Directory, error) {
	row := new(directoryDO)
	found, err := r.engine.Context(ctx).Cols(directoryColumns...).Where(condition+" AND deleted_at = 0", values...).Get(row)
	if err != nil {
		return nil, oops.With("operation", "storage.directory.get").Wrap(err)
	}
	if !found {
		return nil, absent
	}
	var links []shareLinkDO
	err = r.engine.Context(ctx).Cols(shareLinkColumns...).
		Where("owner_key = ? AND directory_name = ? AND deleted_at = 0", row.OwnerKey, row.Name).Asc("created_at", "token").Find(&links)
	if err != nil {
		return nil, oops.With("operation", "storage.share_links.list", "owner_key", row.OwnerKey).Wrap(err)
	}
	return directoryFromDO(row, links)
}

func (r *DirectoryRepository) GetByShareToken(ctx context.Context, token string) (*domain.Directory, error) {
	link := new(shareLinkDO)
	found, err := r.engine.Context(ctx).Cols("owner_key", "directory_name").Where("token = ? AND deleted_at = 0", token).Get(link)
	if err != nil {
		return nil, oops.With("operation", "storage.share_link.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrShareLinkNotFound
	}
	return r.get(ctx, "owner_key = ? AND name = ?", []any{link.OwnerKey, link.DirectoryName}, domain.ErrShareLinkNotFound)
}

func (r *DirectoryRepository) Save(ctx context.Context, directory *domain.Directory) error {
	row := directoryToDO(directory)
	if len(row.OwnerKey) > maxOwnerKeyBytes {
		return domain.ErrInvalidLocator
	}
	session := r.engine.NewSession().Context(ctx)
	defer session.Close()
	if err := session.Begin(); err != nil {
		return oops.With("operation", "storage.directory.begin").Wrap(err)
	}
	if err := saveDirectory(session, row, directory); err != nil {
		_ = session.Rollback()
		return err
	}
	if err := session.Commit(); err != nil {
		return oops.With("operation", "storage.directory.commit", "owner_key", row.OwnerKey).Wrap(err)
	}
	return nil
}

func saveDirectory(tx *xorm.Session, row *directoryDO, directory *domain.Directory) error {
	now := time.Now().UnixMilli()
	row.UpdatedAt = now
	if directory.Version == 0 {
		row.Version, row.CreatedAt = 1, now
		if _, err := tx.Insert(row); err != nil {
			var duplicate *drivermysql.MySQLError
			if errors.As(err, &duplicate) && duplicate.Number == mysqlDuplicateEntry {
				return domain.ErrConcurrentModification
			}
			return oops.With("operation", "storage.directory.insert", "owner_key", row.OwnerKey).Wrap(err)
		}
	} else {
		affected, err := tx.Cols("state", "updated_at").Incr("version").
			Where("owner_key = ? AND name = ? AND version = ? AND deleted_at = 0", row.OwnerKey, row.Name, directory.Version).Update(row)
		if err != nil {
			return oops.With("operation", "storage.directory.update", "owner_key", row.OwnerKey).Wrap(err)
		}
		if affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	for _, link := range directory.ShareLinks {
		data := shareLinkToDO(directory.ID, link)
		_, err := tx.Exec(
			"INSERT INTO storage_share_link (token, owner_key, directory_name, target_name, created_by, state, version, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, 0) "+
				// MySQL assigns left to right: version compares the stored values first.
				"ON DUPLICATE KEY UPDATE version = IF(target_name <> VALUES(target_name) OR state <> VALUES(state), version + 1, version), "+
				"target_name = VALUES(target_name), state = VALUES(state), updated_at = VALUES(updated_at)",
			data.Token, data.OwnerKey, data.DirectoryName, data.TargetName, data.CreatedBy, data.State, data.CreatedAt, now)
		if err != nil {
			return oops.With("operation", "storage.share_link.save", "owner_key", row.OwnerKey).Wrap(err)
		}
	}
	return nil
}
