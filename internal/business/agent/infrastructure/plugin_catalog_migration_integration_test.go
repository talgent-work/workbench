//go:build integration

package infrastructure_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// pluginCatalogMigration is the one-off script that names each retained
// release's catalog and records loaded versions; it and this test are removed
// once every environment ran it.
const pluginCatalogMigration = "../../../../deploy/middleware/migrations/20261112-plugin-catalog.sql"

// A Work stored before releases named their catalog reads again after the
// script, each release naming the catalog the script was given, as do its
// executions' launch specifications, and a Plugin
// outcome recorded as loaded at another version counts as loaded. Applying
// the script again changes nothing.
func TestPluginCatalogMigrationNamesEachRetainedReleasesCatalog(t *testing.T) {
	ctx := context.Background()
	fixture := newBuiltInFixture(t, configuredBuiltIns(t, []string{"talgent"}, nil), releaseCatalog{slug: "talgent", role: "talgent:work-executor", version: "1"})
	definitionID := fixture.projectDefinition(t, "talgent:work-executor", "talgent")
	workID, err := fixture.createProjectWork(definitionID)
	require.NoError(t, err)

	db := fixture.db.DB().DB
	_, err = db.ExecContext(ctx, `UPDATE agent_session SET capabilities = JSON_REMOVE(capabilities, '$.plugins[0].release.catalog_name') WHERE id = ?`, workID)
	require.NoError(t, err)
	_, err = fixture.app.Works.Get(ctx, workID)
	require.Error(t, err, "a release that names no catalog cannot be read")
	// The Work's executions keep the releases their starts took in their
	// launch specification, which an earlier release wrote without a catalog.
	_, err = db.ExecContext(ctx, `INSERT INTO agent_execution (id, session_id, request_json, credential_plan_json, authorization_ids, specification_json, preparation_command, launch_command, native_session_id)
		VALUES (?, ?, '{"Capabilities": {"Plugins": [{"Release": {"Slug": "talgent", "Version": "1"}}]}}', '{}', '[]', '{}', '', '', ?)`, uuid.NewString(), workID, uuid.NewString())
	require.NoError(t, err)
	mismatched := uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO execution_plugin_result (id, execution_id, slug, loading_reported, loaded, loading_reason) VALUES (?, ?, 'talgent', 1, 0, 'version_mismatch')`, mismatched, uuid.NewString())
	require.NoError(t, err)

	script, err := os.ReadFile(pluginCatalogMigration)
	require.NoError(t, err)
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.ExecContext(ctx, `SET @catalog_name = 'company'`)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	work, err := fixture.app.Works.Get(ctx, workID)
	require.NoError(t, err)
	require.Len(t, work.CapabilitySnapshot.Plugins, 1)
	require.Equal(t, "company", work.CapabilitySnapshot.PluginCatalog())
	require.Equal(t, "talgent:work-executor", work.CapabilitySnapshot.LaunchedStartupAgent())

	var catalog string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(request_json, '$.Capabilities.Plugins[0].Release.CatalogName')) FROM agent_execution WHERE session_id = ? LIMIT 1`, workID).Scan(&catalog))
	require.Equal(t, "company", catalog, "an execution's launch specification names the catalog")

	var loaded bool
	var reason, version string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT loaded, loading_reason, loaded_version FROM execution_plugin_result WHERE id = ?`, mismatched).Scan(&loaded, &reason, &version))
	require.True(t, loaded, "a Plugin loaded at another version was loaded")
	require.Empty(t, reason)
	require.Empty(t, version, "the version it loaded was never recorded")
}
