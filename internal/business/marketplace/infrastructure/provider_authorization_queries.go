package infrastructure

import (
	"context"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"xorm.io/xorm"
)

type ProviderAuthorizationQueries struct{ db *xorm.Engine }

var _ query.ProviderAuthorizationQueryRepository = (*ProviderAuthorizationQueries)(nil)

func NewProviderAuthorizationQueries(db *xorm.Engine) *ProviderAuthorizationQueries {
	return &ProviderAuthorizationQueries{db: db}
}

func (r *ProviderAuthorizationQueries) ListPersonalAuthorizations(ctx context.Context, owner string) ([]query.PersonalAuthorization, error) {
	var rows []struct {
		ID       string `xorm:"id"`
		Provider string `xorm:"provider"`
		Status   string `xorm:"status"`
	}
	err := r.db.Context(ctx).Table("scm_provider_authorization").Cols("id", "provider", "status").
		Where("owner_user_id = ? AND deleted_at = 0", owner).Asc("provider", "id").Find(&rows)
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
