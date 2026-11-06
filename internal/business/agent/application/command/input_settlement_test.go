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
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// A settled input is reported to whoever delivered it, by its identity and
// with its outcome; a settled reply is no input's settlement, and a report
// that fails fails the feedback, which is retried.
func TestASettledInputIsReportedWithItsOutcome(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "Patrol Round"}
	buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{input}}
	settlements := &commandtest.InputSettlements{}
	handler := command.NewApplyExecutionFeedbackHandler(time.Now, sessions, exitedProcess(), infrastructure.NewExecutionRequestsFactory(&feedbackPort{}), buffer, settlements)

	buffer.Settle(input.ID)
	require.NoError(t, handler.Handle(ctx, command.ExecutionFeedback{Kind: command.InputSettled, WorkID: sessions.session.ID, ProcessID: "process", InputID: input.ID, InputOutcome: command.InputAbandoned}))
	require.Len(t, settlements.Reported, 1)
	require.Equal(t, command.InputSettlement{WorkID: sessions.session.ID, InputID: input.ID, Outcome: command.InputAbandoned, At: settlements.Reported[0].At}, settlements.Reported[0])

	require.NoError(t, handler.Handle(ctx, command.ExecutionFeedback{Kind: command.InputSettled, WorkID: sessions.session.ID, ProcessID: "process", InputID: "interaction"}))
	require.Len(t, settlements.Reported, 1, "a reply's settlement is not reported")

	require.NoError(t, handler.Handle(ctx, command.ExecutionFeedback{Kind: command.InputSettled, WorkID: sessions.session.ID, ProcessID: "exited-process", InputID: "earlier", InputOutcome: command.InputAbandoned}))
	require.Len(t, settlements.Reported, 2, "an exiting process's settlement is still reported")
	require.Equal(t, "earlier", settlements.Reported[1].InputID)

	settlements.Err = errors.New("bus down")
	require.ErrorIs(t, handler.Handle(ctx, command.ExecutionFeedback{Kind: command.InputSettled, WorkID: sessions.session.ID, ProcessID: "process", InputID: input.ID, InputOutcome: command.InputCompleted}), settlements.Err)
}

// A closing Work's buffered inputs are dropped and each is reported as
// dropped once.
func TestClosingReportsTheInputsItDropped(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	sessions.session.State = domain.WorkClosing
	sessions.session.CloseRequestReference = "close"
	first, second := command.ExecutionInput{ID: uuid.NewString(), Text: "one"}, command.ExecutionInput{ID: uuid.NewString(), Text: "two"}
	buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{first, second}}
	settlements := &commandtest.InputSettlements{}
	recover := command.NewRecoverWorkHandler(time.Now, sessions, infrastructure.NewExecutionRequestsFactory(&startupPort{}), buffer, settlements, nil, nil)

	settlements.Err = errors.New("bus down")
	require.ErrorIs(t, recover.Handle(ctx, sessions.session.ID), settlements.Err)
	require.Zero(t, buffer.Dropped, "a failed report keeps the buffer, so the retry reports again")
	settlements.Err = nil

	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, 1, buffer.Dropped)
	require.Len(t, settlements.Reported, 2)
	for i, input := range []command.ExecutionInput{first, second} {
		require.Equal(t, input.ID, settlements.Reported[i].InputID)
		require.Equal(t, command.InputDropped, settlements.Reported[i].Outcome)
	}
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Len(t, settlements.Reported, 2, "a repeated drop reports nothing more")
}
