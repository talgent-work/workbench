package domain_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func deskReleases(events []domain.Event) []domain.WorkDeskReleased {
	var released []domain.WorkDeskReleased
	for _, event := range events {
		if e, ok := event.(domain.WorkDeskReleased); ok {
			released = append(released, e)
		}
	}
	return released
}

// A run holds its Desk until its process ended and its compute is released.
func TestRunLeavesItsDeskOnceItsComputeIsReleased(t *testing.T) {
	ctx := context.Background()
	work := newSession(t)
	port := &executionPort{}
	require.Equal(t, "01a19bc3-075a-7c57-b42b-7fdf73b611ed", work.ComputerID)
	require.True(t, work.HoldsDesk("desk-test"))
	require.False(t, work.HoldsDesk("another-desk"))

	require.NoError(t, work.RecordExit(ctx, domain.HarnessExit{ExitCode: 1}, port))
	require.True(t, work.HoldsDesk("desk-test"), "an exited process still holds compute until it is released")
	require.Empty(t, deskReleases(work.Events.Drain()))

	require.NoError(t, work.ConfirmComputeReclaimed())
	require.False(t, work.HoldsDesk("desk-test"))
	require.Equal(t, []domain.WorkDeskReleased{{WorkID: work.ID, DeskID: "desk-test"}}, deskReleases(work.Events.Drain()))
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.Empty(t, deskReleases(work.Events.Drain()), "the Desk leaves once")
}

// A start rejected before any compute existed holds no room.
func TestRejectedStartLeavesItsDeskAtOnce(t *testing.T) {
	ctx := context.Background()
	work := newSession(t)
	port := &executionPort{}
	require.NoError(t, work.ReconcileProcess(ctx, domain.ProcessObservation{State: domain.ProcessStartRejected}, port))
	require.False(t, work.HoldsDesk("desk-test"))
	require.Len(t, deskReleases(work.Events.Drain()), 1)
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.Empty(t, deskReleases(work.Events.Drain()))
}

// A Work stays on its Computer: a run admitted elsewhere is refused.
func TestResumeRunsOnlyOnTheWorksComputer(t *testing.T) {
	ctx := context.Background()
	work := newSession(t)
	port := &executionPort{}
	require.NoError(t, work.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, work.ConfirmComputeReclaimed())
	elsewhere := admission(work)
	elsewhere.Seat.ComputerID = "another-computer"
	require.ErrorIs(t, work.Restart(ctx, elsewhere, port), domain.ErrExecutionNotPermitted)
	require.NoError(t, work.Restart(ctx, admission(work), port))
	require.True(t, work.HoldsDesk("desk-test"))
}
