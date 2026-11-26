//go:build integration

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/pkg/database"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// A step that finds another step holding its environment is busy and taken
// again later; it is no conflict with the recorded execution.
func TestAStepFindingItsEnvironmentHeldIsBusy(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	credentials := &SandboxCredentials{locks: database.NewAdvisoryLocks(db.DB().DB)}

	require.NoError(t, credentials.withLock(t.Context(), "sandbox-1", func() error {
		err := credentials.withLock(t.Context(), "sandbox-1", func() error {
			t.Fatal("a held environment ran a second step")
			return nil
		})
		require.ErrorIs(t, err, errEnvironmentBusy)
		require.NotErrorIs(t, err, errEnvironmentConflict)
		return nil
	}))
}
