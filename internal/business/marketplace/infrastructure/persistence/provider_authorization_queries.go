package persistence

import (
	"context"
	"strings"
	"time"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// ProviderAuthorizationQueries reads ProviderAuthorization facts for the
// selections the repository's Get does not serve. It never projects protected
// credentials.
type ProviderAuthorizationQueries struct{ db *xorm.Engine }

var _ query.ProviderAuthorizationQueryRepository = (*ProviderAuthorizationQueries)(nil)

func NewProviderAuthorizationQueries(db *xorm.Engine) *ProviderAuthorizationQueries {
	return &ProviderAuthorizationQueries{db: db}
}

func (q *ProviderAuthorizationQueries) ListPersonalAuthorizations(ctx context.Context, ownerID string) ([]query.PersonalAuthorization, error) {
	var rows []struct {
		ID       string `xorm:"id"`
		Provider string `xorm:"provider"`
		Status   string `xorm:"status"`
	}
	err := q.db.Context(ctx).Table("scm_provider_authorization").Cols("id", "provider", "status").
		Where("owner_id = ? AND deleted_at = 0", ownerID).Asc("provider", "id").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "marketplace.list_personal_authorizations").Wrap(err)
	}
	result := make([]query.PersonalAuthorization, len(rows))
	for i, row := range rows {
		result[i] = query.PersonalAuthorization{ID: row.ID, Provider: row.Provider, Status: row.Status}
		if row.Provider == "github" {
			result[i].SupportedTargets = []string{"github-api"}
		}
	}
	return result, nil
}

func (q *ProviderAuthorizationQueries) GetProviderConnectionState(ctx context.Context, ownerID string, provider domain.Provider) (*query.ProviderConnectionState, error) {
	rows, err := findProviderAuthorizations(ctx, q.db, "owner_id = ? AND provider = ? AND deleted_at = 0", strings.TrimSpace(ownerID), string(provider))
	if err != nil {
		return nil, oops.Wrap(err)
	}
	if len(rows) == 0 {
		return providerConnectionStateWithoutAuthorization(provider), nil
	}
	return providerConnectionStateFromDO(rows[0]), nil
}

func (q *ProviderAuthorizationQueries) ListRefreshCandidates(ctx context.Context, refreshCutoff, leaseCutoff time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	var rows []struct {
		ID string `xorm:"'id'"`
	}
	err := q.db.Context(ctx).Table("scm_provider_authorization").Cols("id").
		Where("status = ? AND deleted_at = 0 AND (refresh_lease_until = 0 OR refresh_lease_until <= ?) AND expires_at > 0 AND expires_at <= ?",
			string(domain.ProviderAuthorizationActive), leaseCutoff.UTC().UnixMilli(), refreshCutoff.UTC().UnixMilli()).
		Asc("expires_at", "id").Limit(min(limit, 1000)).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "marketplace.authorization_maintenance_candidates").Wrap(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

func (q *ProviderAuthorizationQueries) ListScopeObservationCandidates(ctx context.Context, observedBefore time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	var rows []struct {
		ID string `xorm:"'id'"`
	}
	err := q.db.Context(ctx).Table("scm_provider_authorization").Cols("id").
		Where("status = ? AND deleted_at = 0 AND access_scope_observed_at < ?", string(domain.ProviderAuthorizationActive), observedBefore.UTC().UnixMilli()).
		Asc("access_scope_observed_at", "id").Limit(min(limit, 1000)).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "marketplace.scope_observation_candidates").Wrap(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

const maintenancePageSize = 100

type maintenanceIdentity struct {
	ID string `xorm:"'id'"`
}

func (q *ProviderAuthorizationQueries) AuthorizationIDsByAccount(ctx context.Context, provider domain.Provider, accountID, after string) (query.MaintenanceIdentities, error) {
	session := q.db.Context(ctx).Table("scm_provider_authorization").Cols("id").
		Where("provider = ? AND provider_account_id = ? AND id > ? AND deleted_at = 0", string(provider), strings.TrimSpace(accountID), after).Asc("id")
	return maintenanceIdentities(session)
}

func (q *ProviderAuthorizationQueries) AuthorizationIDsByInstallation(ctx context.Context, provider domain.Provider, installationID, after string) (query.MaintenanceIdentities, error) {
	session := q.db.Context(ctx).Table("scm_provider_authorization").Cols("id").
		Where("provider = ? AND CAST(? AS CHAR(32)) MEMBER OF(access_scope_json->'$.installation_ids') AND id > ? AND deleted_at = 0", string(provider), strings.TrimSpace(installationID), after).Asc("id")
	return maintenanceIdentities(session)
}

func maintenanceIdentities(session *xorm.Session) (query.MaintenanceIdentities, error) {
	defer session.Close()
	var rows []maintenanceIdentity
	if err := session.Limit(maintenancePageSize + 1).Find(&rows); err != nil {
		return query.MaintenanceIdentities{}, oops.With("operation", "marketplace.connection_maintenance_candidates").Wrap(err)
	}
	page := query.MaintenanceIdentities{IDs: make([]string, 0, min(len(rows), maintenancePageSize))}
	if len(rows) > maintenancePageSize {
		rows = rows[:maintenancePageSize]
		page.NextCursor = rows[len(rows)-1].ID
	}
	for _, row := range rows {
		page.IDs = append(page.IDs, row.ID)
	}
	return page, nil
}
