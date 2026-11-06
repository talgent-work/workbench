package persistence

import (
	"context"
	"strings"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"xorm.io/xorm"
)

const maintenancePageSize = 100

type maintenanceIdentity struct {
	ID string `xorm:"'id'"`
}

var _ query.AuthorizationEventQueryRepository = (*AuthorizationMaintenanceQueries)(nil)

func (q *AuthorizationMaintenanceQueries) AuthorizationIDsByAccount(ctx context.Context, provider domain.Provider, accountID, after string) (query.MaintenanceIdentities, error) {
	session := q.db.Context(ctx).Table("scm_provider_authorization").Cols("id").
		Where("provider = ? AND provider_account_id = ? AND id > ? AND deleted_at = 0", string(provider), strings.TrimSpace(accountID), after).Asc("id")
	return maintenanceIdentities(session)
}

func (q *AuthorizationMaintenanceQueries) AuthorizationIDsByInstallation(ctx context.Context, provider domain.Provider, installationID, after string) (query.MaintenanceIdentities, error) {
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
