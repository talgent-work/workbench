package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

var _ application.AuthorizationStore = (*ProviderAuthorizations)(nil)

type ProviderAuthorizations struct{ db *xorm.Engine }

const providerAuthorizationSelectColumns = "id, owner_id, provider, provider_account_id, provider_login, access_token_ciphertext, refresh_token_ciphertext, expires_at, refresh_token_expires_at, status, refresh_lease_id, refresh_lease_until, last_refreshed_at, last_checked_at, last_failure_code, last_failure_detail, access_scope_json, access_scope_observed_at, version, created_at, updated_at, deleted_at"

func NewProviderAuthorizations(db *xorm.Engine) *ProviderAuthorizations {
	return &ProviderAuthorizations{db: db}
}

func (r *ProviderAuthorizations) Save(ctx context.Context, authorization *domain.ProviderAuthorization) error {
	row, err := providerAuthorizationToDO(authorization)
	if err != nil {
		return err
	}
	row.UpdatedAt = time.Now().UTC().UnixMilli()
	if authorization.Version == 0 {
		row.CreatedAt = row.UpdatedAt
		row.Version = 1
		if _, err := r.db.Context(ctx).Insert(&row); err != nil {
			return mapDuplicateKey(err, domain.ErrConcurrentModification)
		}
		return nil
	}
	result, err := r.db.Context(ctx).Exec(
		"UPDATE scm_provider_authorization SET provider_account_id = ?, provider_login = ?, access_token_ciphertext = ?, refresh_token_ciphertext = ?, expires_at = ?, refresh_token_expires_at = ?, status = ?, refresh_lease_id = '', refresh_lease_until = 0, last_refreshed_at = ?, last_checked_at = ?, last_failure_code = ?, last_failure_detail = ?, access_scope_json = ?, access_scope_observed_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND deleted_at = 0",
		row.ProviderAccountID, row.ProviderLogin, row.AccessTokenCiphertext, row.RefreshTokenCiphertext, row.ExpiresAt, row.RefreshTokenExpiresAt, row.Status, row.LastRefreshedAt, row.LastCheckedAt, row.LastFailureCode, row.LastFailureDetail, row.AccessScopeJSON, row.AccessScopeObservedAt, row.UpdatedAt, row.ID, row.Version,
	)
	if err != nil {
		return oops.Wrap(err)
	}
	return checkAffected(result, domain.ErrConcurrentModification)
}

func (r *ProviderAuthorizations) Get(ctx context.Context, id string) (*domain.ProviderAuthorization, error) {
	rows, err := findProviderAuthorizations(ctx, r.db, "id = ? AND deleted_at = 0", id)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	if len(rows) == 0 {
		return nil, domain.ErrProviderNotFound
	}
	return providerAuthorizationFromDO(rows[0])
}

func (r *ProviderAuthorizations) GetByOwnerAndProvider(ctx context.Context, ownerID string, provider domain.Provider) (*domain.ProviderAuthorization, error) {
	rows, err := findProviderAuthorizations(ctx, r.db, "owner_id = ? AND provider = ? AND deleted_at = 0", strings.TrimSpace(ownerID), string(provider))
	if err != nil {
		return nil, oops.Wrap(err)
	}
	if len(rows) == 0 {
		return nil, domain.ErrProviderNotFound
	}
	return providerAuthorizationFromDO(rows[0])
}

func (r *ProviderAuthorizations) AcquireCredentialLease(ctx context.Context, authorization *domain.ProviderAuthorization, leaseID string, leaseUntil, now time.Time) (*domain.ProviderAuthorization, error) {
	if authorization == nil || strings.TrimSpace(leaseID) == "" || !leaseUntil.After(now) {
		return nil, domain.ErrInvalidRequiredField
	}
	result, err := r.db.Context(ctx).Exec(
		"UPDATE scm_provider_authorization SET refresh_lease_id = ?, refresh_lease_until = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND deleted_at = 0 AND (refresh_lease_until = 0 OR refresh_lease_until <= ?)",
		leaseID, leaseUntil.UTC().UnixMilli(), now.UTC().UnixMilli(), authorization.ID, authorization.Version, now.UTC().UnixMilli(),
	)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	if err := checkAffected(result, domain.ErrConcurrentModification); err != nil {
		return nil, err
	}
	row, err := providerAuthorizationToDO(authorization)
	if err != nil {
		return nil, err
	}
	row.Version++
	return providerAuthorizationFromDO(row)
}

func providerConnectionStateWithoutAuthorization(provider domain.Provider) *query.ProviderConnectionState {
	return &query.ProviderConnectionState{
		Provider:          string(provider),
		Status:            "none",
		RecommendedAction: "connect",
	}
}

func providerConnectionStateFromDO(row ProviderAuthorizationDO) *query.ProviderConnectionState {
	state := &query.ProviderConnectionState{
		AuthorizationID:             row.ID,
		Provider:                    row.Provider,
		Status:                      row.Status,
		ProviderAccountID:           row.ProviderAccountID,
		ProviderLogin:               row.ProviderLogin,
		RecommendedAction:           "none",
		ReasonCode:                  row.LastFailureCode,
		ReasonMessage:               row.LastFailureDetail,
		AccessTokenExpiresAtUnixMS:  row.ExpiresAt,
		RefreshTokenExpiresAtUnixMS: row.RefreshTokenExpiresAt,
		LastCheckedAtUnixMS:         row.LastCheckedAt,
		LastFailureCode:             row.LastFailureCode,
	}
	switch domain.ProviderAuthorizationStatus(row.Status) {
	case domain.ProviderAuthorizationActive:
		state.CanListRepositories = true
		if row.LastFailureCode != "" {
			state.RecommendedAction = "retry_later"
			state.CanListRepositories = false
		}
	case domain.ProviderAuthorizationReconnectRequired, domain.ProviderAuthorizationRevoked:
		state.RecommendedAction = "reconnect"
	default:
		state.RecommendedAction = "reconnect"
	}
	return state
}

func findProviderAuthorizations(ctx context.Context, db *xorm.Engine, where string, args ...any) ([]ProviderAuthorizationDO, error) {
	var rows []ProviderAuthorizationDO
	query := "SELECT " + providerAuthorizationSelectColumns + " FROM scm_provider_authorization WHERE " + where
	if err := db.Context(ctx).SQL(query, args...).Find(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

type rowsAffectedResult interface {
	RowsAffected() (int64, error)
}

func checkAffected(result rowsAffectedResult, errIfZero error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if affected == 0 {
		return errIfZero
	}
	return nil
}

func mapDuplicateKey(err error, duplicateErr error) error {
	if isDuplicateKey(err) {
		return duplicateErr
	}
	return oops.Wrap(err)
}

func isDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
