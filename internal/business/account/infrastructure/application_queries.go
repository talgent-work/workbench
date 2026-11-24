package infrastructure

import (
	"context"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// ApplicationQueries reads Applications by their Application Owner.
type ApplicationQueries struct {
	db *jimuxorm.DB
}

var _ query.ApplicationQueryRepository = (*ApplicationQueries)(nil)

func NewApplicationQueries(db *jimuxorm.DB) *ApplicationQueries {
	return &ApplicationQueries{db: db}
}

// OwnedApplications reads the owner's Applications, removed ones included,
// through idx_owner_registered, then their secrets still valid at now.
func (r *ApplicationQueries) OwnedApplications(ctx context.Context, owner string, now time.Time) ([]query.OwnedApplication, error) {
	var rows []applicationDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Cols("id", "name", "registered_at", "state").
		Where("owner_id = ? AND deleted_at = 0", owner).
		OrderBy("registered_at, id").Find(&rows); err != nil {
		return nil, oops.With("operation", "application.list_owned").Wrap(err)
	}
	if len(rows) == 0 {
		return []query.OwnedApplication{}, nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	var secrets []clientSecretDO
	at := now.UnixMilli()
	session, err = r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Cols(clientSecretColumns...).In("application_id", ids).
		Where("deleted_at = 0 AND (state = ? OR (state = ? AND overlap_ends_at > ?))",
			int(domain.ClientSecretCurrent), int(domain.ClientSecretOverlapping), at).
		OrderBy("state, issued_at, id").Find(&secrets); err != nil {
		return nil, oops.With("operation", "application.list_owned_secrets").Wrap(err)
	}
	bySecret := map[string][]query.ClientSecret{}
	for _, secret := range secrets {
		bySecret[secret.ApplicationID] = append(bySecret[secret.ApplicationID], ownedClientSecretFromDO(secret))
	}
	owned := make([]query.OwnedApplication, 0, len(rows))
	for _, row := range rows {
		owned = append(owned, query.OwnedApplication{
			ID: row.ID, Name: row.Name, RegisteredAt: fromMillis(row.RegisteredAt),
			Secrets: append([]query.ClientSecret{}, bySecret[row.ID]...), Removed: row.State == int(domain.ApplicationRemoved),
		})
	}
	return owned, nil
}

func ownedClientSecretFromDO(row clientSecretDO) query.ClientSecret {
	state := query.ClientSecretCurrent
	if row.State == int(domain.ClientSecretOverlapping) {
		state = query.ClientSecretOverlapping
	}
	return query.ClientSecret{
		ID: row.ID, State: state, Hint: row.Hint, IssuedAt: fromMillis(row.IssuedAt),
		LastUsedAt: fromMillis(row.LastUsedAt), OverlapEndsAt: fromMillis(row.OverlapEndsAt),
	}
}
