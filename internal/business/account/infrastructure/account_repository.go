package infrastructure

import (
	"context"
	"errors"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/go-sql-driver/mysql"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

const mysqlDuplicateEntry = 1062

var accountColumns = []string{"id", "github_user_id", "username", "email", "display_name", "avatar_url", "last_signed_in_at", "version", "created_at"}

// AccountRepository persists Accounts.
type AccountRepository struct {
	db *jimuxorm.DB
}

var _ domain.AccountRepository = (*AccountRepository)(nil)

func NewAccountRepository(db *jimuxorm.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) Get(ctx context.Context, id domain.AccountID) (*domain.Account, error) {
	return r.find(ctx, "account.get", "id = ? AND deleted_at = 0", string(id))
}

func (r *AccountRepository) FindByGitHubBinding(ctx context.Context, binding domain.GitHubBinding) (*domain.Account, error) {
	return r.find(ctx, "account.find_by_github_binding", "github_user_id = ? AND deleted_at = 0", int64(binding))
}

func (r *AccountRepository) find(ctx context.Context, operation, where string, arg any) (*domain.Account, error) {
	row := new(accountDO)
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(accountColumns...).Where(where, arg).Get(row)
	if err != nil {
		return nil, oops.With("operation", operation).Wrap(err)
	}
	if !found {
		return nil, domain.ErrAccountNotFound
	}
	account, err := accountFromDO(row)
	if err != nil {
		return nil, oops.With("operation", operation+".rehydrate").With("account_id", row.ID).Wrap(err)
	}
	return account, nil
}

func (r *AccountRepository) Save(ctx context.Context, account *domain.Account) error {
	row := accountToDO(account)
	now := time.Now().UnixMilli()
	row.UpdatedAt = now
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	if account.Version == 0 {
		row.Version = 1
		if _, err := session.Insert(row); err != nil {
			return classifyAccountInsert(err, string(account.ID))
		}
		return nil
	}
	affected, err := session.
		Cols("username", "email", "display_name", "avatar_url", "last_signed_in_at", "updated_at").
		Incr("version").
		Where("id = ? AND version = ? AND deleted_at = 0", row.ID, account.Version).
		Update(row)
	if err != nil {
		return oops.With("operation", "account.update").With("account_id", row.ID).Wrap(err)
	}
	if affected != 1 {
		return domain.ErrConcurrentModification
	}
	return nil
}

func classifyAccountInsert(err error, accountID string) error {
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == mysqlDuplicateEntry {
		return domain.ErrGitHubBindingTaken
	}
	return oops.With("operation", "account.insert").With("account_id", accountID).Wrap(err)
}
