//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A run its Managed Computer fenced retires on its Work's request without
// its exit reaching the execution; the release deletes the run's processes
// once the machine is back. A repeated request changes nothing.
func TestAFencedManagedRunRetiresWithoutReclaimingCompute(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)

	stopped := managedRequest().WorkID
	startManaged(t, f, stopped)
	require.NoError(t, f.steps.retire(ctx, stopped, domain.RetiredFenced, time.Now()))
	_, live, err := f.provisioning.live(ctx, stopped)
	require.NoError(t, err)
	require.False(t, live, "the Work's request retires its fenced run")
	retired, err := f.steps.executions.Latest(ctx, stopped)
	require.NoError(t, err)
	require.False(t, retired.Process.Exited, "no exit reached the execution")
	require.NoError(t, f.steps.retire(ctx, stopped, domain.RetiredFenced, time.Now()))
	again, err := f.steps.executions.Latest(ctx, stopped)
	require.NoError(t, err)
	require.Equal(t, retired.Version, again.Version, "a repeated request finds nothing to retire")

	lost := "01a19bc3-075a-7c57-b42b-7fdf73b61200"
	record := startManaged(t, f, lost)
	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	require.NoError(t, f.steps.retire(ctx, lost, domain.RetiredFenced, time.Now()))
	require.NoError(t, f.steps.reconcile(ctx, record.ID))
	pending, err := unreleased(ctx, f.db)
	require.NoError(t, err)
	require.NotEmpty(t, pending, "the runs' processes are released once the machine is back")
	f.relay.mu.Lock()
	require.Empty(t, f.relay.deleted)
	f.relay.mu.Unlock()

	f.relay.set(func(m *machineRelay) { m.unreachable = false })
	require.NoError(t, f.steps.reconcile(ctx, record.ID))
	f.relay.mu.Lock()
	require.Contains(t, f.relay.deleted, "harness-handle")
	f.relay.mu.Unlock()
	require.Equal(t, domain.ExecutionReleased, current(t, f.steps, record.ID).State)
}
