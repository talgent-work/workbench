package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func closedEvents(events []domain.Event) []domain.WorkClosedEvent {
	var closed []domain.WorkClosedEvent
	for _, event := range events {
		if e, ok := event.(domain.WorkClosedEvent); ok {
			closed = append(closed, e)
		}
	}
	return closed
}

// Stopping keeps the workspace; reaching Closed records WorkClosed once, the
// fact on which Storage is asked to reclaim it. Agent keeps no obligation.
func TestReachingClosedRecordsWorkClosedOnce(t *testing.T) {
	ctx := context.Background()
	s := newSession(t)
	execution := &executionPort{}

	require.NoError(t, recoverStartup(ctx, s, execution))
	require.NoError(t, s.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, s.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, s.Stop(ctx, "stop", execution))
	require.NoError(t, s.RecordExit(ctx, domain.HarnessExit{}, execution))
	require.NoError(t, s.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, s.State)
	require.Empty(t, closedEvents(s.Events.Drain()), "a stopped Work keeps its workspace")
	// Each command records on a freshly loaded Work.
	s.Events = domain.NewEventCollection()

	require.NoError(t, s.Close(ctx, "archive", domain.ClosureFacts{}, execution))
	require.Equal(t, domain.WorkClosed, s.State)
	require.Equal(t, []domain.WorkClosedEvent{{WorkID: s.ID, WorkspaceReference: "workspace-a"}}, closedEvents(s.Events.Drain()))
	require.Equal(t, "user-home", s.HomeBinding.OwnerID)
	s.Events = domain.NewEventCollection()

	require.NoError(t, s.Close(ctx, "repeat-close", domain.ClosureFacts{}, execution))
	require.Equal(t, "archive", s.CloseRequestReference, "repetition preserves the originally accepted close")
	require.Empty(t, closedEvents(s.Events.Drain()), "a repeated close asks for nothing again")
	require.ErrorIs(t, s.Resume(ctx, admission(s), execution), domain.ErrWorkTransition)
}
