package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Computer Removed fails every Work running on the Computer, from what
// Computer says now: a fact about a Computer that is not removed, or one
// repeated, changes nothing, and nothing is asked of the gone machine.
func TestComputerRemovedLosesTheEnvironmentOfItsWorks(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	place := &presencePlace{description: command.ComputerDescription{Managed: true, Reachable: true}}
	reach := reachOver(w, time.Now, place)

	require.NoError(t, reach.ComputerRemoved(ctx, sessions.session.ComputerID))
	require.Equal(t, domain.WorkWorking, sessions.session.State, "a Computer that is not removed loses nothing")

	place.description = command.ComputerDescription{Managed: true, Removed: true}
	require.NoError(t, reach.ComputerRemoved(ctx, sessions.session.ComputerID))
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.Equal(t, domain.ComputerRemovedReason, sessions.session.FailureReason)
	saves := sessions.saves
	require.NoError(t, reach.ComputerRemoved(ctx, sessions.session.ComputerID))
	require.Equal(t, saves, sessions.saves, "a repeated fact changes nothing")
	require.True(t, w.execution(t).Outcome.Lost, "the execution records the loss it reported")
	require.Equal(t, domain.ComputerRemovedReason, w.execution(t).Outcome.LossReason)
	require.Empty(t, w.environment.Deleted, "nothing is asked of the gone machine")

	place.err = errors.New("computer unavailable")
	require.Error(t, reach.ComputerRemoved(ctx, sessions.session.ComputerID), "the consumer retries a fact it could not apply")
}

// A Computer Removed that never arrived is applied by the Work's recovery.
func TestRecoveryAppliesAMissedComputerRemoval(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	place := &presencePlace{description: command.ComputerDescription{Managed: true, Removed: true}}
	reach := reachOver(w, time.Now, place)
	recover := recoverOver(w, time.Now, reach)

	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.True(t, sessions.session.HarnessProcess.EnvironmentLost)
	reads := place.reads
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, reads, place.reads, "a Work whose environment is gone asks no more")
}
