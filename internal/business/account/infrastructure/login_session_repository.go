package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

var loginSessionColumns = []string{"id", "account_id", "refresh_digest", "expires_at", "absolute_expires_at", "revoked_at", "last_seen_at", "user_agent", "version"}

// LoginSessionRepository persists Login Sessions with the refresh secrets they
// replaced, so a replaced secret still finds its session.
type LoginSessionRepository struct {
	engine *xorm.Engine
}

var _ domain.LoginSessionRepository = (*LoginSessionRepository)(nil)

func NewLoginSessionRepository(engine *xorm.Engine) *LoginSessionRepository {
	return &LoginSessionRepository{engine: engine}
}

func (r *LoginSessionRepository) Get(ctx context.Context, id string) (*domain.LoginSession, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrLoginSessionNotFound
	}
	return r.load(ctx, "id = ? AND deleted_at = 0", id)
}

func (r *LoginSessionRepository) FindByRefreshSecret(ctx context.Context, digest domain.RefreshSecretDigest) (*domain.LoginSession, domain.PresentedRefreshSecret, error) {
	session, err := r.load(ctx, "refresh_digest = ? AND deleted_at = 0", string(digest))
	if err == nil {
		return session, domain.PresentedRefreshSecret{Digest: digest}, nil
	}
	if !errors.Is(err, domain.ErrLoginSessionNotFound) {
		return nil, domain.PresentedRefreshSecret{}, err
	}
	replaced := new(replacedSecretDO)
	found, err := r.engine.Context(ctx).Cols("session_id").
		Where("refresh_digest = ? AND deleted_at = 0", string(digest)).Get(replaced)
	if err != nil {
		return nil, domain.PresentedRefreshSecret{}, oops.With("operation", "login_session.find_replaced").Wrap(err)
	}
	if !found {
		return nil, domain.PresentedRefreshSecret{}, domain.ErrLoginSessionNotFound
	}
	session, err = r.load(ctx, "id = ? AND deleted_at = 0", replaced.SessionID)
	if err != nil {
		return nil, domain.PresentedRefreshSecret{}, err
	}
	return session, domain.PresentedRefreshSecret{Digest: digest, Replaced: true}, nil
}

func (r *LoginSessionRepository) load(ctx context.Context, where string, arg any) (*domain.LoginSession, error) {
	row := new(loginSessionDO)
	found, err := r.engine.Context(ctx).Cols(loginSessionColumns...).Where(where, arg).Get(row)
	if err != nil {
		return nil, oops.With("operation", "login_session.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrLoginSessionNotFound
	}
	session, err := loginSessionFromDO(row)
	if err != nil {
		return nil, oops.With("operation", "login_session.rehydrate").With("session_id", row.ID).Wrap(err)
	}
	return session, nil
}

// Save writes the session row and, after a Refresh, records the secret it
// replaced so presenting it again is recognized as reuse.
func (r *LoginSessionRepository) Save(ctx context.Context, session *domain.LoginSession) error {
	dbSession := r.engine.NewSession().Context(ctx)
	defer dbSession.Close()
	if err := dbSession.Begin(); err != nil {
		return oops.With("operation", "login_session.begin").Wrap(err)
	}
	if err := saveLoginSession(dbSession, session); err != nil {
		_ = dbSession.Rollback()
		return err
	}
	if err := dbSession.Commit(); err != nil {
		return oops.With("operation", "login_session.commit").With("session_id", session.ID).Wrap(err)
	}
	return nil
}

func saveLoginSession(tx *xorm.Session, session *domain.LoginSession) error {
	now := time.Now().UnixMilli()
	row := loginSessionToDO(session)
	row.UpdatedAt = now
	if session.Version == 0 {
		row.Version = 1
		row.CreatedAt = now
		if _, err := tx.Insert(row); err != nil {
			return oops.With("operation", "login_session.insert").With("session_id", session.ID).Wrap(err)
		}
	} else {
		affected, err := tx.
			Cols("refresh_digest", "expires_at", "revoked_at", "last_seen_at", "updated_at").
			Incr("version").
			Where("id = ? AND version = ? AND deleted_at = 0", row.ID, session.Version).
			Update(row)
		if err != nil {
			return oops.With("operation", "login_session.update").With("session_id", session.ID).Wrap(err)
		}
		if affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	if session.ReplacedSecret == "" {
		return nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if _, err := tx.Insert(&replacedSecretDO{
		ID: id.String(), SessionID: session.ID, RefreshDigest: string(session.ReplacedSecret),
		ExpiresAt: millis(session.AbsoluteExpiresAt), Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return oops.With("operation", "login_session.record_replaced").With("session_id", session.ID).Wrap(err)
	}
	return nil
}

// ForgetReplacedSecrets drops reuse evidence of sessions past their absolute
// expiry, oldest first and at most limit rows; such sessions issue nothing.
func (r *LoginSessionRepository) ForgetReplacedSecrets(ctx context.Context, expiredBefore time.Time, limit int) (int64, error) {
	result, err := r.engine.Context(ctx).Exec(
		"DELETE FROM login_session_replaced_secret WHERE expires_at < ? ORDER BY expires_at, id LIMIT ?",
		expiredBefore.UnixMilli(), limit,
	)
	if err != nil {
		return 0, oops.With("operation", "login_session.forget_replaced").Wrap(err)
	}
	return result.RowsAffected()
}
