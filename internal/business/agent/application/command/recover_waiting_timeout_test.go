package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Recovery weighs the Work's buffer for a due waiting timeout: a buffered
// reply defers it, while input held behind the unanswered request does not,
// so the stop asks for the held Turn to be cancelled before the exit signal.
func TestRecoveryWeighsTheBufferForADueWaitingTimeout(t *testing.T) {
	ctx := context.Background()
	held := func(t *testing.T) (*feedbackSessionStore, time.Time) {
		_, sessions := runningMessagingSession(t)
		origin := time.Now().Add(-domain.WaitingTimeout - time.Minute)
		require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, origin))
		require.NoError(t, sessions.session.ApplyResponseRequirement(1000, true, origin))
		return sessions, origin
	}
	t.Run("reply buffered", func(t *testing.T) {
		sessions, _ := held(t)
		buffer := &commandtest.InputBuffer{Replies: []command.InteractionResponse{{InteractionID: "question"}}}
		require.NoError(t, command.NewRecoverWorkHandler(time.Now, sessions, buffer, command.NewInputAcceptance(sessions, buffer), &commandtest.InputSettlements{}, nil).Handle(ctx, sessions.session.ID))
		require.True(t, sessions.session.Waiting())
		require.Empty(t, sessions.committedStops())
	})
	t.Run("input held behind the request", func(t *testing.T) {
		sessions, _ := held(t)
		buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{{ID: uuid.NewString(), Text: "held"}}}
		require.NoError(t, command.NewRecoverWorkHandler(time.Now, sessions, buffer, command.NewInputAcceptance(sessions, buffer), &commandtest.InputSettlements{}, nil).Handle(ctx, sessions.session.ID))
		require.Equal(t, domain.WorkStopping, sessions.session.State)
		require.Equal(t, domain.StopReasonWaitingTimeout, sessions.session.StopReason)
		stops := sessions.committedStops()
		require.Len(t, stops, 1)
		require.True(t, stops[0].CancelWaitingTurn, "the held Turn is cancelled first")
	})
}

// Turn activity reported after the Turn ended clears idleness the ended Turn
// recorded; recovery restores it once nothing awaits processing, so a late
// report delays the timeout by at most a sweep.
func TestRecoveryRestoresIdlenessALateActivityReportEnded(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1}, false, false, time.Now()))
	require.True(t, sessions.session.ObserveTurnActivity())
	buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{{ID: uuid.NewString(), Text: "queued"}}}
	recover := command.NewRecoverWorkHandler(time.Now, sessions, buffer, command.NewInputAcceptance(sessions, buffer), &commandtest.InputSettlements{}, nil)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.False(t, sessions.session.Idle(), "buffered input is still to be processed")
	buffer.Settle(buffer.Inputs[0].ID)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.True(t, sessions.session.Idle())
}
