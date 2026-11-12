//go:build integration

package infrastructure_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// authorizationOwnerMigration is the one-off script that renames the Provider
// Authorizations' owner column; it and this test are removed once every
// environment ran it.
const authorizationOwnerMigration = "../../../../deploy/middleware/migrations/20261116-marketplace-authorization-owner.sql"

// An authorization recorded under owner_user_id keeps its owner and its one
// authorization per owner and Provider under owner_id after the script;
// applying it again, or to the canonical schema, changes nothing.
func TestAuthorizationOwnerMigrationRenamesTheOwnerColumn(t *testing.T) {
	ctx := context.Background()
	engine, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	db := engine.DB().DB
	script, err := os.ReadFile(authorizationOwnerMigration)
	require.NoError(t, err)
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// The schema returns to its canonical shape for the tests that borrow it
	// next, whatever this one leaves.
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), string(script)) })
	_, err = conn.ExecContext(ctx, string(script))
	require.NoError(t, err, "the canonical schema already has owner_id")

	for _, statement := range []string{
		"ALTER TABLE scm_provider_authorization RENAME COLUMN `owner_id` TO `owner_user_id`",
		"INSERT INTO scm_provider_authorization (id, owner_user_id, provider, status, access_token_ciphertext, refresh_token_ciphertext) VALUES ('0193f1a4-7c2e-7b10-8a3d-000000000011', 'alice', 'github', 'active', '', '')",
	} {
		_, err = conn.ExecContext(ctx, statement)
		require.NoError(t, err)
	}
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	var owned int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM scm_provider_authorization WHERE owner_id = 'alice' AND provider = 'github'").Scan(&owned))
	require.Equal(t, 1, owned)
	_, err = conn.ExecContext(ctx, "INSERT INTO scm_provider_authorization (id, owner_id, provider, status, access_token_ciphertext, refresh_token_ciphertext) VALUES ('0193f1a4-7c2e-7b10-8a3d-000000000012', 'alice', 'github', 'active', '', '')")
	require.Error(t, err, "an owner still has one authorization per Provider")
	_, err = conn.ExecContext(ctx, "DELETE FROM scm_provider_authorization WHERE id = '0193f1a4-7c2e-7b10-8a3d-000000000011'")
	require.NoError(t, err)
}
