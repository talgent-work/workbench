package infrastructure

import (
	"context"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/account/application"
)

// ClientSecretUses writes a Client Secret's last use on its own row, beside
// the Application's version: grants race only on that row, and the forward
// condition makes the latest use win whatever order they commit in.
type ClientSecretUses struct {
	db *jimuxorm.DB
}

var _ application.ClientSecretUses = (*ClientSecretUses)(nil)

func NewClientSecretUses(db *jimuxorm.DB) *ClientSecretUses {
	return &ClientSecretUses{db: db}
}

func (r *ClientSecretUses) RecordLastUse(ctx context.Context, applicationID, secretID string, at time.Time, resolution time.Duration) error {
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	// No row changes when a use within resolution is already recorded, or
	// when the secret is gone; either way the use needs no write.
	if _, err := session.Exec(
		"UPDATE application_client_secret SET last_used_at = ?, updated_at = ? WHERE id = ? AND application_id = ? AND deleted_at = 0 AND last_used_at <= ?",
		at.UnixMilli(), time.Now().UnixMilli(), secretID, applicationID, at.Add(-resolution).UnixMilli(),
	); err != nil {
		return oops.With("operation", "client_secret.record_last_use").With("application_id", applicationID).Wrap(err)
	}
	return nil
}
