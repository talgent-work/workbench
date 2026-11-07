//go:build integration

package execution

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// fencedWorks holds the Work's state as Agent records it: Working until the
// test ends its run as its machine's fence did.
type fencedWorks struct {
	mu    sync.Mutex
	ended map[string]bool
}

func (w *fencedWorks) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended[id] {
		return &domain.AgentWork{ID: id, State: domain.WorkStopped, HarnessProcess: &domain.HarnessProcess{Exited: true, ComputeReclaimed: true}}, nil
	}
	return &domain.AgentWork{ID: id, State: domain.WorkWorking, HarnessProcess: &domain.HarnessProcess{}}, nil
}

func (*fencedWorks) Save(context.Context, *domain.AgentWork) error { return nil }

func (w *fencedWorks) fence(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ended[id] = true
}

// startManaged starts and connects a Managed Work's run and answers its
// allocation.
func startManaged(t *testing.T, f *managedWork, workID string) allocationRecord {
	t.Helper()
	ctx := context.Background()
	input := managedRequest()
	input.WorkID = workID
	input.ContextMode = domain.ContextFresh
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	record, _, err := latestAllocation(ctx, f.db, workID)
	require.NoError(t, err)
	require.False(t, record.ProcessExited)
	return record
}

// A run its Managed Computer fenced retires on the Work's request without a
// compute reclamation and without its exit reaching the allocation; the
// release deletes the run's processes once the machine is back. A request
// while the Work still holds the run changes nothing, and recovery retires
// the allocation when the request was lost.
func TestAFencedManagedRunRetiresWithoutReclaimingCompute(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	works := &fencedWorks{ended: map[string]bool{}}
	held := NewHeldWorks(f.db, works)
	f.provisioning.credentials.works = held
	f.provisioning.managed.works = held

	stopped := managedRequest().WorkID
	startManaged(t, f, stopped)
	require.NoError(t, f.port.RetireFencedRun(ctx, stopped))
	_, live, err := f.provisioning.live(ctx, stopped)
	require.NoError(t, err)
	require.True(t, live, "a run the Work still holds is not retired")
	works.fence(stopped)
	require.NoError(t, f.port.RetireFencedRun(ctx, stopped))
	_, live, err = f.provisioning.live(ctx, stopped)
	require.NoError(t, err)
	require.False(t, live, "the Work's request retires its fenced run")
	require.NoError(t, f.port.RetireFencedRun(ctx, stopped), "a repeated request finds nothing to retire")

	lost := "01a19bc3-075a-7c57-b42b-7fdf73b61200"
	record := startManaged(t, f, lost)
	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	works.fence(lost)
	_, err = f.recovery.ReconcileAllocation(ctx, record.ID)
	require.NoError(t, err)
	_, live, err = f.provisioning.live(ctx, lost)
	require.NoError(t, err)
	require.False(t, live, "recovery retires a fenced run whose request was lost")
	pending, err := unreleased(ctx, f.db)
	require.NoError(t, err)
	require.NotEmpty(t, pending, "the runs' processes are released once the machine is back")
	require.Empty(t, f.relay.deleted)

	f.relay.set(func(m *machineRelay) { m.unreachable = false })
	_, err = f.recovery.ReconcileAllocation(ctx, record.ID)
	require.NoError(t, err)
	require.Contains(t, f.relay.deleted, "harness-handle")
}
