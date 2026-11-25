package infrastructure

import (
	"context"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// ownedClientSecretColumns are what an owner reads of a Client Secret: never
// its digest.
var ownedClientSecretColumns = []string{"id", "application_id", "hint", "state", "issued_at", "overlap_ends_at", "last_used_at"}

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
	if err := session.Cols(ownedClientSecretColumns...).In("application_id", ids).
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

// SecretLastUses reads the last use of each of the Application's secrets that
// obtained a token, by the application_id index.
func (r *ApplicationQueries) SecretLastUses(ctx context.Context, applicationID string) (map[string]time.Time, error) {
	var rows []clientSecretDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Cols("id", "last_used_at").
		Where("application_id = ? AND deleted_at = 0 AND last_used_at > 0", applicationID).Find(&rows); err != nil {
		return nil, oops.With("operation", "application.secret_last_uses").With("application_id", applicationID).Wrap(err)
	}
	uses := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		uses[row.ID] = fromMillis(row.LastUsedAt)
	}
	return uses, nil
}
