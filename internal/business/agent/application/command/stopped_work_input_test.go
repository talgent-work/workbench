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

// Input to a Work that is not running, such as a reviewer's
// requested changes, is kept for its next start: the process of its next
// Resume or Restart reads it, and keeping it never starts the Work.
func TestInputIsKeptForTheNextStartOfAWorkThatIsNotRunning(t *testing.T) {
	ctx := context.Background()
	for _, state := range []domain.WorkState{domain.WorkStarting, domain.WorkStopping, domain.WorkStopped, domain.WorkFailed} {
		t.Run(string(state), func(t *testing.T) {
			_, sessions := runningMessagingSession(t)
			buffer := &commandtest.InputBuffer{}
			deliver := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, buffer))
			sessions.session.State = state
			input := command.ExecutionInput{ID: uuid.NewString(), Text: "Requested changes"}
			request := command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: input}

			require.NoError(t, deliveryErr(deliver.Deliver(ctx, request)))
			require.NoError(t, deliveryErr(deliver.Deliver(ctx, request)), "a repeat is the same input")
			require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs)
			require.Equal(t, state, sessions.session.State, "keeping input never starts the Work")
		})
	}
}

// A Disconnection changes no admission: input to a Working Work whose
// environment is not observed is accepted into its buffer, which the Harness
// reads once its stream is attached again.
func TestInputIsAcceptedDuringADisconnection(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	buffer := &commandtest.InputBuffer{}
	deliver := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, buffer))
	sessions.session.DisconnectedSince = time.Now().Add(-time.Minute)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "Keep going"}
	require.NoError(t, deliveryErr(deliver.Deliver(ctx, command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: input})))
	require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs)
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	require.True(t, sessions.session.Disconnected(), "accepting input does not end the Disconnection")
}

func TestInputIsRefusedForAWorkBeingClosed(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	buffer := &commandtest.InputBuffer{}
	deliver := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, buffer))
	sessions.session.State, sessions.session.CloseRequestReference = domain.WorkClosing, "close"
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "Requested changes"}
	require.ErrorIs(t, deliveryErr(deliver.Deliver(ctx, command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: input})), domain.ErrWorkTransition)
	require.Empty(t, buffer.Inputs)
}

// deliveryErr keeps only the error of a delivery.
func deliveryErr(_ command.InputHandOff, err error) error { return err }
