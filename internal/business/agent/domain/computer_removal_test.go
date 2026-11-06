package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The removal of a Work's Managed Computer is the loss of its execution
// environment: Working becomes Failed and Closing becomes Closed, each
// returning its Desk, with nothing asked of the gone machine.
func TestAComputerRemovalLosesTheEnvironmentOfItsWorks(t *testing.T) {
	ctx := context.Background()
	working, port := turningWork(t)
	changed, err := working.ApplyComputerRemoval(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkFailed, working.State)
	require.Equal(t, domain.ComputerRemovedReason, working.FailureReason)
	require.True(t, working.HarnessProcess.EnvironmentLost)
	require.False(t, working.HoldsProcess())
	require.Contains(t, eventKinds(working), domain.EventKindDeskReleased)
	require.Empty(t, port.stops)
	require.Zero(t, port.reclaims)

	changed, err = working.ApplyComputerRemoval(ctx)
	require.NoError(t, err)
	require.False(t, changed, "applying the removal again changes nothing")

	closing, port := runningHarnessWork(t, domain.HarnessChoice{})
	require.NoError(t, closing.RecordFailure("model unavailable"))
	require.NoError(t, closing.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosing, closing.State)
	changed, err = closing.ApplyComputerRemoval(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkClosed, closing.State)
	require.Equal(t, "model unavailable", closing.FailureReason, "an earlier failure keeps explaining it")
}

// A start still waiting in a removed Computer's line ends, and a Work that
// stopped before the removal keeps its State: it can only be closed.
func TestAComputerRemovalEndsAWaitingStartAndLeavesAStoppedWork(t *testing.T) {
	ctx := context.Background()
	queued, port := queuedWork(t, time.Now())
	changed, err := queued.ApplyComputerRemoval(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkFailed, queued.State)
	require.False(t, queued.WaitsForDesk())
	require.Empty(t, port.starts, "nothing starts on a removed Computer")

	stopped, _ := stoppedWork(t)
	changed, err = stopped.ApplyComputerRemoval(ctx)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, domain.WorkStopped, stopped.State)
}

func eventKinds(work *domain.AgentWork) []domain.EventKind {
	var kinds []domain.EventKind
	for _, event := range work.Events.Drain() {
		kinds = append(kinds, event.Kind())
	}
	return kinds
}
