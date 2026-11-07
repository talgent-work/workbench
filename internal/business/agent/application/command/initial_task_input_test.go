package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// workHoldingInitialTaskInput is a new Work whose initial task input has not
// reached its input buffer yet, as its creation commits it.
func workHoldingInitialTaskInput(t *testing.T) (*feedbackSessionStore, domain.InitialTaskInput) {
	t.Helper()
	_, sessions := startupStores(t)
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "first task\n保留原文")
	require.NoError(t, err)
	sessions.session.InitialTaskInput = &initial
	return sessions, initial
}

func pendingInitialTaskInput(sessions *feedbackSessionStore) bool {
	_, pending := sessions.session.PendingInitialTaskInput()
	return pending
}

// Later input never overtakes the initial task input: accepting it first
// hands the initial task input to the buffer, even when the creation's answer
// was lost before anything handed it off (Initial task input order).
func TestLaterInputFollowsThePendingInitialTaskInput(t *testing.T) {
	ctx := context.Background()
	sessions, initial := workHoldingInitialTaskInput(t)
	buffer := &commandtest.InputBuffer{}
	deliver := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, buffer))
	later := command.ExecutionInput{ID: uuid.NewString(), Text: "follow-up"}

	handOff, err := deliver.Deliver(ctx, command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: later})
	require.NoError(t, err)
	require.Equal(t, command.InputBuffered, handOff)
	require.Equal(t, []command.ExecutionInput{{ID: initial.ID, Text: initial.Text}, later}, buffer.Inputs)
	require.False(t, pendingInitialTaskInput(sessions), "the hand-off is committed with the later input")
	require.Equal(t, domain.WorkStarting, sessions.session.State, "input never starts the Work")
}

// A replayed later input still commits the hand-off it made of the initial
// task input.
func TestReplayedLaterInputCommitsTheInitialTaskInputHandOff(t *testing.T) {
	ctx := context.Background()
	sessions, initial := workHoldingInitialTaskInput(t)
	later := command.ExecutionInput{ID: uuid.NewString(), Text: "follow-up"}
	buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{later}}
	deliver := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, buffer))

	handOff, err := deliver.Deliver(ctx, command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: later})
	require.NoError(t, err)
	require.Equal(t, command.InputRepeated, handOff)
	require.Equal(t, initial.ID, buffer.Inputs[1].ID)
	require.False(t, pendingInitialTaskInput(sessions))
}

// Work recovery hands off a pending initial task input idempotently by its
// identity: a failed buffer or a lost record keeps it pending, and the retry
// buffers it once.
func TestWorkRecoveryHandsOffAPendingInitialTaskInput(t *testing.T) {
	ctx := context.Background()
	sessions, initial := workHoldingInitialTaskInput(t)
	buffer := &commandtest.InputBuffer{}
	recover := command.NewRecoverWorkHandler(time.Now, sessions, buffer, command.NewInputAcceptance(sessions, buffer), &commandtest.InputSettlements{}, nil)

	buffer.Err = errors.New("buffer unavailable")
	require.ErrorIs(t, recover.Handle(ctx, sessions.session.ID), buffer.Err)
	require.True(t, pendingInitialTaskInput(sessions))
	buffer.Err = nil

	sessions.saveErr = errors.New("database unavailable after the hand-off")
	require.ErrorIs(t, recover.Handle(ctx, sessions.session.ID), sessions.saveErr)
	require.True(t, pendingInitialTaskInput(sessions), "a lost record keeps the initial task input pending")
	sessions.saveErr = nil

	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.False(t, pendingInitialTaskInput(sessions))
	require.Equal(t, []command.ExecutionInput{{ID: initial.ID, Text: initial.Text}}, buffer.Inputs, "buffered once under its own identity")
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Len(t, buffer.Inputs, 1, "a handed-off initial task input is not handed off again")
}
