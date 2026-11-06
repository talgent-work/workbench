package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Only an allocation that never bound a process and is past its Startup
// Deadline is abandoned, prepared or not; a newer start's allocation never is.
func TestAbandonedAllocation(t *testing.T) {
	now := time.Now()
	old := now.Add(-domain.StartupDeadline - abandonmentMargin).UnixMilli()
	require.True(t, abandoned(allocationRecord{CreatedAt: old}, now))
	require.True(t, abandoned(allocationRecord{CreatedAt: old, EnvironmentPrepared: true}, now), "launch is refused past the deadline even after preparation")
	require.False(t, abandoned(allocationRecord{CreatedAt: now.Add(-domain.StartupDeadline).UnixMilli()}, now), "a launch begun at the deadline may still record its process")
	require.False(t, abandoned(allocationRecord{CreatedAt: now.Add(-time.Minute).UnixMilli()}, now), "a start within its deadline may still prepare")
	require.False(t, abandoned(allocationRecord{CreatedAt: old, ProcessID: "process"}, now), "a bound process ends by its exit")
	require.False(t, abandoned(allocationRecord{}, now))
}
