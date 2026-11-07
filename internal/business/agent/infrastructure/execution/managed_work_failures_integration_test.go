//go:build integration

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
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
	require.NoError(t, f.port.StartExecution(ctx, input))

	require.NoError(t, submitPending(ctx, f.provisioning))
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, record.Submitted)
	require.Empty(t, record.SandboxID, "an unreachable machine opened nothing")
	require.False(t, record.PreparationRecovering, "a machine away is waited for, not a failure")

	f.relay.set(func(m *machineRelay) { m.unreachable = false })
	require.NoError(t, submitPending(ctx, f.provisioning))
	record, _, err = latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, record.ID, record.SandboxID, "the allocation stands for the environment it opened")
	require.False(t, record.PreparationRecovering)
}

// The release of a retired Managed allocation stays outstanding while its
// machine is away, and settles without a call once Computer says the machine
// was removed: nothing is left there to release.
func TestManagedReleaseWaitsForTheMachineAndSettlesOnItsRemoval(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	exited, err := f.observations.AcknowledgeExit(ctx, input.WorkID, "harness-handle")
	require.NoError(t, err)
	require.True(t, exited)

	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	require.NoError(t, f.port.ReclaimCompute(ctx, input.WorkID))
	_, err = f.recovery.ReconcileAllocation(ctx, record.ID)
	require.NoError(t, err)
	pending, err := unreleased(ctx, f.db)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the run's processes are released once the machine is back")
	require.Empty(t, f.feedback.requests, "no release is confirmed before it happened")
	// The Work's own recovery observes the exited run meanwhile without
	// asking Dispatcher, which holds no connection of an exited process.
	waiting, err := f.observations.ObserveProcess(ctx, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, domain.ProcessPending, waiting.State)
	require.False(t, waiting.ComputeReclaimed)

	f.place.removed = true
	_, err = f.recovery.ReconcileAllocation(ctx, record.ID)
	require.NoError(t, err)
	pending, err = unreleased(ctx, f.db)
	require.NoError(t, err)
	require.Empty(t, pending)
	require.Len(t, f.feedback.requests, 1)
	require.Equal(t, command.ComputeReclaimed, f.feedback.requests[0].Kind)
	require.Empty(t, f.relay.deleted, "a removed machine is not called")
	observation, err := f.observations.ObserveProcess(ctx, input.WorkID)
	require.NoError(t, err)
	require.True(t, observation.ComputeReclaimed)
}
