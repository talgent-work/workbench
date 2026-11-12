package infrastructure

import (
	"context"
	"time"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

var (
	applicationColumns  = []string{"id", "owner_id", "name", "state", "registered_at", "version"}
	clientSecretColumns = []string{"id", "application_id", "secret_digest", "hint", "state", "issued_at", "overlap_ends_at", "last_used_at"}
)

// ApplicationRepository persists Applications with their Client Secrets,
// which change only with their Application.
type ApplicationRepository struct {
	engine *xorm.Engine
}

var _ domain.ApplicationRepository = (*ApplicationRepository)(nil)

func NewApplicationRepository(engine *xorm.Engine) *ApplicationRepository {
	return &ApplicationRepository{engine: engine}
}

func (r *ApplicationRepository) Get(ctx context.Context, id domain.ApplicationID) (*domain.Application, error) {
	row := new(applicationDO)
	found, err := r.engine.Context(ctx).Cols(applicationColumns...).Where("id = ? AND deleted_at = 0", string(id)).Get(row)
	if err != nil {
		return nil, oops.With("operation", "application.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrApplicationNotFound
	}
	var secrets []clientSecretDO
	if err := r.engine.Context(ctx).Cols(clientSecretColumns...).
		Where("application_id = ? AND deleted_at = 0", row.ID).OrderBy("issued_at, id").Find(&secrets); err != nil {
		return nil, oops.With("operation", "application.get_secrets").With("application_id", row.ID).Wrap(err)
	}
	application, err := applicationFromDO(row, secrets)
	if err != nil {
		// A stored Application the domain refuses is a storage fault, not a
		// caller's invalid input, so the domain error stays out of its chain.
		return nil, oops.With("operation", "application.rehydrate").With("application_id", row.ID).
			Errorf("stored application is not valid: %v", err)
	}
	return application, nil
}

// Save writes the Application and its secrets together: a new Application
// with all of them, a loaded one under its version, adding the secrets it
// issued since it was loaded.
func (r *ApplicationRepository) Save(ctx context.Context, application *domain.Application) error {
	session := r.engine.NewSession().Context(ctx)
	defer session.Close()
	if err := session.Begin(); err != nil {
		return oops.With("operation", "application.begin").Wrap(err)
	}
	if err := saveApplication(session, application); err != nil {
		_ = session.Rollback()
		return err
	}
	if err := session.Commit(); err != nil {
		return oops.With("operation", "application.commit").With("application_id", string(application.ID)).Wrap(err)
	}
	return nil
}

func saveApplication(tx *xorm.Session, application *domain.Application) error {
	now := time.Now().UnixMilli()
	row := applicationToDO(application)
	row.UpdatedAt = now
	stored := map[string]bool{}
	if application.Version == 0 {
		row.Version = 1
		row.CreatedAt = now
		if _, err := tx.Insert(row); err != nil {
			return oops.With("operation", "application.insert").With("application_id", row.ID).Wrap(err)
		}
	} else {
		affected, err := tx.Cols("name", "state", "updated_at").Incr("version").
			Where("id = ? AND version = ? AND deleted_at = 0", row.ID, application.Version).Update(row)
		if err != nil {
			return oops.With("operation", "application.update").With("application_id", row.ID).Wrap(err)
		}
		if affected != 1 {
			return domain.ErrConcurrentModification
		}
		var ids []string
		if err := tx.Table(clientSecretDO{}).Cols("id").Where("application_id = ? AND deleted_at = 0", row.ID).Find(&ids); err != nil {
			return oops.With("operation", "application.stored_secrets").With("application_id", row.ID).Wrap(err)
		}
		for _, id := range ids {
			stored[id] = true
		}
	}
	for _, secret := range application.Secrets {
		secretRow := clientSecretToDO(application.ID, secret)
		secretRow.UpdatedAt = now
		if stored[secret.ID] {
			if _, err := tx.Cols("state", "overlap_ends_at", "last_used_at", "updated_at").Incr("version").
				Where("id = ? AND application_id = ? AND deleted_at = 0", secret.ID, row.ID).Update(secretRow); err != nil {
				return oops.With("operation", "application.update_secret").With("application_id", row.ID).Wrap(err)
			}
			continue
		}
		secretRow.Version = 1
		secretRow.CreatedAt = now
		if _, err := tx.Insert(secretRow); err != nil {
			return oops.With("operation", "application.insert_secret").With("application_id", row.ID).Wrap(err)
		}
	}
	return nil
}
