package persistence

import (
	"context"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"xorm.io/xorm"
)

type AuthorizationMaintenanceQueries struct{ db *xorm.Engine }

var _ query.AuthorizationMaintenanceQueryRepository = (*AuthorizationMaintenanceQueries)(nil)

func NewAuthorizationMaintenanceQueries(db *xorm.Engine) *AuthorizationMaintenanceQueries {
	return &AuthorizationMaintenanceQueries{db: db}
}

func (q *AuthorizationMaintenanceQueries) ListRefreshCandidates(ctx context.Context, refreshCutoff, leaseCutoff time.Time, limit int) ([]string, error) {
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

func (q *AuthorizationMaintenanceQueries) ListScopeObservationCandidates(ctx context.Context, observedBefore time.Time, limit int) ([]string, error) {
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
