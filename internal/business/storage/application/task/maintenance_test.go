package task

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The Mapped View is repaired every hour, at half past, UTC, on the
// maintenance lane.
func TestTheMappedViewIsRepairedEveryHour(t *testing.T) {
	periodic, err := NewRepairMappedView()
	require.NoError(t, err)
	require.NoError(t, periodic.Validate())
	require.Equal(t, "30 * * * *", periodic.Schedule().Spec())
	require.Equal(t, "UTC", periodic.Schedule().Location())
	require.Equal(t, RepairMappedViewType, periodic.Task().Type())
	require.Equal(t, MaintenanceQueue, periodic.Task().Queue())
}
