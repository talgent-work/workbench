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

// A live start is abandoned past its deadline once its create was submitted,
// whether or not the sandbox's identity was learned. One that never submitted
// a create, a rejected one and a retired one are not.
func TestAbandonedStart(t *testing.T) {
	now := time.Now()
	old := now.Add(-domain.StartupDeadline - abandonmentMargin).UnixMilli()
	require.True(t, startAbandoned(allocationRecord{CreatedAt: old, Submitted: true}, now), "the sandbox's identity is not needed")
	require.True(t, startAbandoned(allocationRecord{CreatedAt: old, Submitted: true, SandboxID: "sandbox"}, now))
	require.False(t, startAbandoned(allocationRecord{CreatedAt: old}, now), "no create was submitted")
	require.False(t, startAbandoned(allocationRecord{CreatedAt: old, Submitted: true, StartRejected: true}, now), "a rejected start is settled as rejected")
	require.False(t, startAbandoned(allocationRecord{CreatedAt: old, Submitted: true, DeletedAt: old}, now), "a retired allocation is no longer a start")
	require.False(t, startAbandoned(allocationRecord{CreatedAt: now.UnixMilli(), Submitted: true}, now), "a start within its deadline waits")
}
