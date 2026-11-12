//go:build integration

package persistence

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"xorm.io/xorm"
)

func testConnectionMaintenancePages(t *testing.T, db *xorm.Engine) {
	ctx := t.Context()
	authRepo, authQueries := NewProviderAuthorizations(db), NewProviderAuthorizationQueries(db)
	var authorizationIDs []string
	for n := range 105 {
		auth := newProviderAuthorizationForTest(t, fmt.Sprintf("event-owner-%03d", n))
		auth.ProviderAccountID = "event-account"
		require.NoError(t, authRepo.Save(ctx, auth))
		authorizationIDs = append(authorizationIDs, auth.ID)
	}
	foreign := newProviderAuthorizationForTest(t, "foreign-owner")
	require.NoError(t, authRepo.Save(ctx, foreign))
	collect := func(fetch func(string) (query.MaintenanceIdentities, error)) []string {
		t.Helper()
		var ids []string
		for after := ""; ; {
			page, err := fetch(after)
			require.NoError(t, err)
			require.LessOrEqual(t, len(page.IDs), 100)
			for _, id := range page.IDs {
				require.Greater(t, id, after)
			}
			ids = append(ids, page.IDs...)
			if page.NextCursor == "" {
				return ids
			}
			require.Len(t, page.IDs, 100)
			require.Equal(t, page.IDs[len(page.IDs)-1], page.NextCursor)
			require.Greater(t, page.NextCursor, after)
			after = page.NextCursor
		}
	}
	byAccount := func(after string) (query.MaintenanceIdentities, error) {
		return authQueries.AuthorizationIDsByAccount(ctx, domain.ProviderGitHub, "event-account", after)
	}
	slices.Sort(authorizationIDs)
	require.Equal(t, authorizationIDs, collect(byAccount))
	// Execute the real revocation webhook use case with these adapters and
	// verify durable aggregate changes on both sides of the page boundary.
	connections := application.NewPersonalConnections(authRepo, authQueries, nil, nil, nil)
	require.NoError(t, connections.HandleGitHubAppAuthorizationRevoked(ctx, application.GitHubAppAuthorizationRevokedInput{ProviderAccountID: "event-account"}))
	for _, id := range authorizationIDs {
		auth, err := authRepo.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, domain.ProviderAuthorizationRevoked, auth.Status)
	}
	currentForeign, err := authRepo.Get(ctx, foreign.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderAuthorizationActive, currentForeign.Status)

	_, err = db.Exec("UPDATE scm_provider_authorization SET deleted_at=1,version=version+1 WHERE owner_id='event-owner-000'")
	require.NoError(t, err)
	require.Len(t, collect(byAccount), 104)
}
