//go:build integration

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Managed allocation's environment is recorded only once the machine opened
// it. A machine the relay does not reach leaves it unrecorded and not
// recovering.
func TestManagedSubmissionWaitsForTheMachine(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	begun := f.begin(t, input)

	long, err := f.steps.handler.Reconcile(ctx, begun.ID)
	require.NoError(t, err)
	require.True(t, long, "the creation is submitted by the long step")
	require.ErrorIs(t, f.steps.handler.RunLongStep(ctx, begun.ID), errCreationUnresolved, "the machine away answers nothing yet")
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, record.submitted())
	require.Empty(t, record.SandboxID, "an unreachable machine opened nothing")
	require.False(t, record.PreparationRecovering, "a machine away is waited for, not a failure")

	f.relay.set(func(m *machineRelay) { m.unreachable = false })
	require.NoError(t, f.steps.reconcile(ctx, begun.ID))
	record, _, err = latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, record.ID, record.SandboxID, "the execution stands for the environment it opened")
	require.False(t, record.PreparationRecovering)
}

// The release of a retired Managed execution stays outstanding while its
// machine is away, and settles without a call once Computer says the machine
// was removed: nothing is left there to release.
func TestManagedReleaseWaitsForTheMachineAndSettlesOnItsRemoval(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	record := f.launch(t, f.begin(t, input).ID)
	f.exit(t, record)

	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	require.NoError(t, f.steps.reconcile(ctx, record.ID))
	pending, err := unreleased(ctx, f.db)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the run's processes are released once the machine is back")
	waiting := current(t, f.steps, record.ID)
	require.Equal(t, domain.ExecutionRetired, waiting.State, "no release is confirmed before it happened")
	require.True(t, waiting.Desk.LeftAt.IsZero(), "its Desk is held until its processes are gone")

	f.place.removed = true
	require.NoError(t, f.steps.reconcile(ctx, record.ID))
	pending, err = unreleased(ctx, f.db)
	require.NoError(t, err)
	require.Empty(t, pending)
	released := current(t, f.steps, record.ID)
	require.True(t, reported(released, domain.ExecutionReleasedEvent{WorkID: input.WorkID, StartAdmittedAt: released.StartAdmittedAt}))
	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	require.Empty(t, f.relay.deleted, "a removed machine is not called")
}
