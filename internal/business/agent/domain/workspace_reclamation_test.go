package domain_test

import (
	"testing"

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
	s := newSession(t)

	ready(t, s)
	require.NoError(t, s.Stop("stop", false))
	exited(t, s, domain.HarnessExit{})
	released(t, s)
	require.Equal(t, domain.WorkStopped, s.State)
	require.Empty(t, closedEvents(s.Events.Drain()), "a stopped Work keeps its workspace")
	// Each command records on a freshly loaded Work.
	s.Events = domain.NewEventCollection()

	require.NoError(t, s.Close("archive", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosed, s.State)
	require.Equal(t, []domain.WorkClosedEvent{{WorkID: s.ID, Cloud: true}}, closedEvents(s.Events.Drain()))
	s.Events = domain.NewEventCollection()

	require.NoError(t, s.Close("repeat-close", domain.ClosureFacts{}))
	require.Equal(t, "archive", s.CloseRequestReference, "repetition preserves the originally accepted close")
	require.Empty(t, closedEvents(s.Events.Drain()), "a repeated close asks for nothing again")
	require.ErrorIs(t, s.Resume(admission(s)), domain.ErrWorkTransition)
}
