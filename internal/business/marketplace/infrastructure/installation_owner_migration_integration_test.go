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

// installationOwnerMigration is the one-off script that renames the
// installations' owner column; it and this test are removed once every
// environment ran it.
const installationOwnerMigration = "../../../../deploy/middleware/migrations/20261115-marketplace-installation-owner.sql"

// Installations recorded under user_id keep their owner and uniqueness under
// owner_id after the script; applying it again, or to the canonical schema,
// changes nothing.
func TestInstallationOwnerMigrationRenamesTheOwnerColumn(t *testing.T) {
	ctx := context.Background()
	engine, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	db := engine.DB().DB
	script, err := os.ReadFile(installationOwnerMigration)
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
		"ALTER TABLE marketplace_installed_plugin RENAME COLUMN `owner_id` TO `user_id`, RENAME INDEX `uniq_owner_id_slug` TO `uniq_user_id_slug`",
		"ALTER TABLE marketplace_installed_connector RENAME COLUMN `owner_id` TO `user_id`, RENAME INDEX `uniq_owner_id_connector_id` TO `uniq_user_id_connector_id`",
		"INSERT INTO marketplace_installed_plugin (id, user_id, slug, state, configuration) VALUES ('0193f1a4-7c2e-7b10-8a3d-000000000001', 'alice', 'reviewer', 1, '{}')",
		"INSERT INTO marketplace_installed_connector (id, user_id, connector_id, state) VALUES ('0193f1a4-7c2e-7b10-8a3d-000000000002', 'alice', '0193f1a4-7c2e-7b10-8a3d-000000000003', 2)",
	} {
		_, err = conn.ExecContext(ctx, statement)
		require.NoError(t, err)
	}
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	var plugins, connectors int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM marketplace_installed_plugin WHERE owner_id = 'alice' AND slug = 'reviewer'").Scan(&plugins))
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM marketplace_installed_connector WHERE owner_id = 'alice' AND state = 2").Scan(&connectors))
	require.Equal(t, [2]int{1, 1}, [2]int{plugins, connectors})
	_, err = conn.ExecContext(ctx, "INSERT INTO marketplace_installed_plugin (id, owner_id, slug, state, configuration) VALUES ('0193f1a4-7c2e-7b10-8a3d-000000000004', 'alice', 'reviewer', 1, '{}')")
	require.Error(t, err, "an owner still has one installation per slug")
}
