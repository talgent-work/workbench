package infrastructure

import (
	"context"
	"errors"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
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
	directoryColumns = []string{"owner_key", "name", "backend", "lifetime", "state", "space_token", "granted_read_write", "version"}
	mappingColumns   = []string{"path", "owner_key", "directory_name"}
	shareLinkColumns = []string{"token", "owner_key", "directory_name", "file_id", "created_by", "created_by_application", "state", "created_at"}
)

// DirectoryRepository persists Directories with their Share Links and
// Directory Mappings; the Directory row's version serializes every change of
// one Directory, and a mapped path's key keeps two Directories from being
// mapped at one path.
type DirectoryRepository struct {
	db *jimuxorm.DB
}

var _ domain.DirectoryRepository = (*DirectoryRepository)(nil)

func NewDirectoryRepository(db *jimuxorm.DB) *DirectoryRepository {
	return &DirectoryRepository{db: db}
}

func (r *DirectoryRepository) Get(ctx context.Context, id domain.DirectoryID) (*domain.Directory, error) {
	return r.get(ctx, "owner_key = ? AND name = ?", []any{ownerKey(id.Owner), id.Name}, domain.ErrDirectoryNotOpen)
}

func (r *DirectoryRepository) GetBySpaceToken(ctx context.Context, token string) (*domain.Directory, error) {
	return r.get(ctx, "space_token = ?", []any{token}, domain.ErrSpaceTokenRejected)
}

func (r *DirectoryRepository) get(ctx context.Context, condition string, values []any, absent error) (*domain.Directory, error) {
	row := new(directoryDO)
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(directoryColumns...).Where(condition+" AND deleted_at = 0", values...).Get(row)
	if err != nil {
		return nil, oops.With("operation", "storage.directory.get").Wrap(err)
	}
	if !found {
		return nil, absent
	}
	var links []shareLinkDO
	session, err = r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	err = session.Cols(shareLinkColumns...).
		Where("owner_key = ? AND directory_name = ? AND deleted_at = 0", row.OwnerKey, row.Name).Asc("created_at", "token").Find(&links)
	if err != nil {
		return nil, oops.With("operation", "storage.share_links.list", "owner_key", row.OwnerKey).Wrap(err)
	}
	var mappings []mappingDO
	session, err = r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	err = session.Cols(mappingColumns...).
		Where("owner_key = ? AND directory_name = ?", row.OwnerKey, row.Name).Asc("path").Find(&mappings)
	if err != nil {
		return nil, oops.With("operation", "storage.mappings.list", "owner_key", row.OwnerKey).Wrap(err)
	}
	return directoryFromDO(row, links, mappings)
}

func (r *DirectoryRepository) GetByShareToken(ctx context.Context, token string) (*domain.Directory, error) {
	link := new(shareLinkDO)
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols("owner_key", "directory_name").Where("token = ? AND deleted_at = 0", token).Get(link)
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
	return r.db.Manager().WithinOrJoin(ctx, func(ctx context.Context) error {
		session, err := r.db.Session(ctx)
		if err != nil {
			return err
		}
		return saveDirectory(session, row, directory)
	})
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
		affected, err := tx.Cols("state", "granted_read_write", "updated_at").Incr("version").
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
			"INSERT INTO storage_share_link (token, owner_key, directory_name, file_id, created_by, created_by_application, state, version, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, 0) "+
				// A link's file never changes; only its state does. MySQL
				// assigns left to right: version compares the stored state first.
				"ON DUPLICATE KEY UPDATE version = IF(state <> VALUES(state), version + 1, version), "+
				"state = VALUES(state), updated_at = VALUES(updated_at)",
			data.Token, data.OwnerKey, data.DirectoryName, data.FileID, data.CreatedBy, data.CreatedByApplication, data.State, data.CreatedAt, now)
		if err != nil {
			return oops.With("operation", "storage.share_link.save", "owner_key", row.OwnerKey).Wrap(err)
		}
	}
	return saveMappings(tx, row, directory, now)
}

// saveMappings makes the Directory's mapping rows its mapped paths. A path
// another Directory holds refuses the save as an overlap.
func saveMappings(tx *xorm.Session, row *directoryDO, directory *domain.Directory, now int64) error {
	var stored []mappingDO
	if err := tx.Cols("path").Where("owner_key = ? AND directory_name = ?", row.OwnerKey, row.Name).Find(&stored); err != nil {
		return oops.With("operation", "storage.mappings.list", "owner_key", row.OwnerKey).Wrap(err)
	}
	kept := make(map[string]bool, len(stored))
	for _, mapping := range stored {
		if directory.MappedAt(domain.MappedPath(mapping.Path)) {
			kept[mapping.Path] = true
			continue
		}
		if _, err := tx.Where("path = ? AND owner_key = ? AND directory_name = ?", mapping.Path, row.OwnerKey, row.Name).Delete(new(mappingDO)); err != nil {
			return oops.With("operation", "storage.mapping.delete", "owner_key", row.OwnerKey).Wrap(err)
		}
	}
	for _, path := range directory.Mappings {
		if kept[string(path)] {
			continue
		}
		_, err := tx.Insert(&mappingDO{Path: string(path), OwnerKey: row.OwnerKey, DirectoryName: row.Name, CreatedAt: now})
		var duplicate *drivermysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == mysqlDuplicateEntry {
			return domain.ErrMappingOverlaps
		}
		if err != nil {
			return oops.With("operation", "storage.mapping.insert", "owner_key", row.OwnerKey).Wrap(err)
		}
	}
	return nil
}
