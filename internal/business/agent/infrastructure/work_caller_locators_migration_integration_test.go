//go:build integration

package infrastructure_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// workCallerLocatorsMigration is the one-off script that renames the Locator
// conditions stored in AgentDefinitions' Work Layouts; it and this test are
// removed once every environment ran it.
const workCallerLocatorsMigration = "../../../../deploy/middleware/migrations/20261113-work-caller-locators.sql"

// A definition stored before Works fixed their Work Caller names its inputs
// and knowledge mounts by the old conditions "subject" and "project", which
// the current layout refuses. After the script its layout is the default one,
// following the File Home and the Work Caller, and a Work of it starts.
// Applying the script again changes nothing.
func TestWorkCallerLocatorsMigrationRenamesStoredConditions(t *testing.T) {
	ctx := context.Background()
	f := newBuiltInFixture(t, configuredBuiltIns(t, nil, nil), &builtInCatalog{})
	definitionID := f.projectDefinition(t, "")
	db := f.db.DB().DB

	_, err := db.ExecContext(ctx, `UPDATE agent_definition
	   SET work_layout = CAST(REPLACE(REPLACE(CAST(work_layout AS CHAR), '"file_home"', '"subject"'), '"work_caller"', '"project"') AS JSON)
	 WHERE id = ?`, definitionID)
	require.NoError(t, err)
	_, err = f.app.Definitions.Get(ctx, definitionID)
	require.Error(t, err, "the stored layout names conditions the current layout refuses")

	script, err := os.ReadFile(workCallerLocatorsMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = db.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	definition, err := f.app.Definitions.Get(ctx, definitionID)
	require.NoError(t, err)
	require.Equal(t, domain.DefaultWorkLayout(), definition.WorkLayout)
	_, err = f.createProjectWork(definitionID)
	require.NoError(t, err)
}
