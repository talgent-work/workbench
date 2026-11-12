//go:build integration

package infrastructure_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// workSelectionOwnerMigration is the one-off script that renames the owner key
// of stored Work selections; it and this test are removed once every
// environment ran it.
const workSelectionOwnerMigration = "../../../../deploy/middleware/migrations/20261116-agent-work-selection-owner.sql"

// A Work stored with the previous owner_user_id key reads back with the same
// owner after the script, and applying the script again changes nothing.
func TestWorkSelectionOwnerMigrationKeepsTheOwner(t *testing.T) {
	f := newWorkCreationFixture(t)
	handler := command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels()))
	id, err := handler.Handle(t.Context(), f.request)
	require.NoError(t, err)
	before, err := f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "owner", before.RepositorySelection.OwnerID)

	// The stored form the previous release wrote.
	for _, column := range []string{"repository_selection", "authorization_selection"} {
		_, err = f.db.Exec("UPDATE agent_session SET "+column+" = JSON_REMOVE(JSON_SET("+column+", '$.owner_user_id', JSON_EXTRACT("+column+", '$.owner_id')), '$.owner_id') WHERE id = ? AND JSON_CONTAINS_PATH("+column+", 'one', '$.owner_id')", id)
		require.NoError(t, err)
	}
	var legacy int
	_, err = f.db.SQL("SELECT COUNT(*) FROM agent_session WHERE id = ? AND JSON_CONTAINS_PATH(repository_selection, 'one', '$.owner_user_id')", id).Get(&legacy)
	require.NoError(t, err)
	require.Equal(t, 1, legacy)

	script, err := os.ReadFile(workSelectionOwnerMigration)
	require.NoError(t, err)
	conn, err := f.db.DB().DB.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	for range 2 {
		_, err = conn.ExecContext(t.Context(), string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	after, err := f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, before.RepositorySelection, after.RepositorySelection)
	require.Equal(t, before.AuthorizationSelection, after.AuthorizationSelection)
}
