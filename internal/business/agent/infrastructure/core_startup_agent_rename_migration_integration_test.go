//go:build integration

package infrastructure_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// coreStartupAgentRenameMigration is the one-off script that renames the core
// plugin's stored Startup Agent; it and this test are removed once every
// environment ran it.
const coreStartupAgentRenameMigration = "../../../../deploy/middleware/migrations/20261113-core-startup-agent-rename.sql"

// A deployment holding the core plugin's old Startup Agent in a definition and
// a Work names the new one after the script, in the definition, the Work's
// Startup Agent and the Agents its retained release declares. Applying the
// script again changes nothing.
func TestCoreStartupAgentRenameMigrationNamesWorkExecutor(t *testing.T) {
	const oldRole, newRole = "talgent:personal-work-executor", "talgent:work-executor"
	ctx := context.Background()
	fixture := newBuiltInFixture(t, configuredBuiltIns(t, []string{"talgent"}, nil), releaseCatalog{slug: "talgent", role: oldRole, version: "1"})
	definitionID := fixture.projectDefinition(t, oldRole, "talgent")
	workID, err := fixture.createProjectWork(definitionID)
	require.NoError(t, err)

	script, err := os.ReadFile(coreStartupAgentRenameMigration)
	require.NoError(t, err)
	conn, err := fixture.db.DB().DB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	definition, err := fixture.app.Definitions.Get(ctx, definitionID)
	require.NoError(t, err)
	require.Equal(t, newRole, definition.WorkDefaults.StartupAgent)

	work, err := fixture.app.Works.Get(ctx, workID)
	require.NoError(t, err)
	require.Equal(t, newRole, work.CapabilitySnapshot.StartupAgent)
	require.Len(t, work.CapabilitySnapshot.Plugins, 1)
	require.Equal(t, []domain.AgentComponent{{Name: newRole, Description: "Work executor"}}, work.CapabilitySnapshot.Plugins[0].Release.Agents)
	require.NoError(t, work.CapabilitySnapshot.Validate())
}
