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
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Closing a running Work stops it first and closes it once stopped; an
// unknown commit outcome is retried with the same request.
func TestCloseStopsARunningWorkAndClosesItOnceStopped(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
	facts := &completionFacts{}
	buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{{ID: uuid.NewString(), Text: "still buffered"}}}
	port := &controlRecorder{}
	handler := command.NewCloseWorkHandler(sessions, sessionAccess{}, port, facts, buffer)
	request := command.WorkControl{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString()}
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrWorkTransition, "buffered input is not discarded")
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	buffer.Settle(buffer.Inputs[0].ID)
	failure := errors.New("commit unavailable")
	sessions.saveErr = failure
	require.ErrorIs(t, handler.Handle(ctx, request), failure)
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	require.Empty(t, port.stops, "nothing is sent before the commit")
	sessions.saveErr = nil
	require.NoError(t, handler.Handle(ctx, request))
	require.Equal(t, domain.WorkStopping, sessions.session.State, "a running Work is stopped first")
	require.Equal(t, request.RequestID, sessions.session.CloseRequestReference)
	require.Equal(t, []string{request.RequestID}, port.stops)
	another := request
	another.RequestID = uuid.NewString()
	require.NoError(t, handler.Handle(ctx, another), "a later close is acknowledged")
	require.Equal(t, request.RequestID, sessions.session.CloseRequestReference, "the first close is the one that applies")
	require.NoError(t, handler.ApplyKeptClose(ctx, sessions.session.ID), "still stopping: nothing to apply yet")
	require.Equal(t, domain.WorkStopping, sessions.session.State)

	require.NoError(t, sessions.session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, sessions.session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.NoError(t, handler.ApplyKeptClose(ctx, sessions.session.ID))
	require.Equal(t, domain.WorkClosed, sessions.session.State)
	require.Equal(t, request.RequestID, sessions.session.CloseRequestReference)
	require.Contains(t, sessions.session.Events.Drain(), domain.Event(domain.WorkClosingEntered{WorkID: sessions.session.ID}), "entering Closing empties the input buffer through its event")
	facts.err = errors.New("startup query unavailable after acceptance")
	require.NoError(t, handler.Handle(ctx, request), "a repeated close confirms the accepted one")
}

func TestCloseRefusesOutstandingWorkButNotUnknownFacts(t *testing.T) {
	for _, stage := range []string{"continuation", "initial task input", "unknown"} {
		t.Run(stage, func(t *testing.T) {
			_, sessions := runningMessagingSession(t)
			require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
			facts := &completionFacts{}
			failure := errors.New("startup unavailable")
			switch stage {
			case "continuation":
				facts.continuation.Pending = true
			case "initial task input":
				initial, err := domain.NewInitialTaskInput(uuid.NewString(), "first task")
				require.NoError(t, err)
				sessions.session.InitialTaskInput = &initial
			case "unknown":
				facts.err = failure
			}
			handler := command.NewCloseWorkHandler(sessions, sessionAccess{}, &controlRecorder{}, facts, &commandtest.InputBuffer{})
			err := handler.Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString()})
			if stage == "unknown" {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, domain.ErrWorkTransition)
			}
			require.Equal(t, domain.WorkWorking, sessions.session.State)
		})
	}
}

// A failed Work whose compute is not yet released closes with input still
// buffered, and entering Closing drops that input.
func TestCloseDropsTheBufferedInputOfAFailedWork(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	require.NoError(t, sessions.session.RecordFailure("harness crashed"))
	require.False(t, sessions.session.HarnessProcess.ComputeReclaimed)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "still buffered"}
	buffer := &commandtest.InputBuffer{Inputs: []command.ExecutionInput{input}}
	facts := &completionFacts{}
	port := &controlRecorder{}
	handler := command.NewCloseWorkHandler(sessions, sessionAccess{}, port, facts, buffer)
	require.NoError(t, handler.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString()}))
	require.Equal(t, domain.WorkClosing, sessions.session.State)

	settlements := &commandtest.InputSettlements{}
	recover := command.NewRecoverWorkHandler(time.Now, sessions, &startupPort{}, buffer, command.NewInputAcceptance(sessions, buffer), settlements, nil, nil)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Empty(t, buffer.Inputs, "the buffered input is dropped")
	require.Len(t, settlements.Reported, 1)
	require.Equal(t, input.ID, settlements.Reported[0].InputID)
	require.Equal(t, command.InputDropped, settlements.Reported[0].Outcome)
}

// controlRecorder records the stops a command sends.
type controlRecorder struct {
	controlPort
	stops []string
}

func (p *controlRecorder) StopExecution(_ context.Context, _ string, id string) error {
	p.stops = append(p.stops, id)
	return nil
}

// Input is accepted once Dispatcher has buffered it: a failed hand-off changes
// nothing, a buffered one ends idleness and records the human input, and a
// repeated or settled identity changes no Work state.
func TestInputIsAcceptedOnceBufferedAndItsIdentityIsOneInput(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	waitAt := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, waitAt))
	require.True(t, sessions.session.Idle())
	buffer := &commandtest.InputBuffer{Err: errors.New("Dispatcher unavailable")}
	inputs := command.NewInputAcceptance(sessions, buffer)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "  next task\n继续 "}
	require.ErrorIs(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)), buffer.Err)
	require.True(t, sessions.session.Idle(), "a failed hand-off changes nothing")

	buffer.Err = nil
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)))
	require.False(t, sessions.session.Idle())
	require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs, "the text is buffered exactly as submitted")

	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 2, Completed: true}, false, false, waitAt.Add(time.Minute)))
	saves := sessions.saves
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)))
	buffer.Settle(input.ID)
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)))
	require.True(t, sessions.session.Idle(), "a repeated identity ends no newer idle interval")
	require.Equal(t, saves, sessions.saves)

	platform := command.ExecutionInput{ID: uuid.NewString(), Text: "A notice arrived"}
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, platform)))
	require.False(t, sessions.session.Idle())
	for _, invalid := range []command.ExecutionInput{{ID: "not-a-uuid", Text: "x"}, {ID: uuid.NewString(), Text: " \n"}} {
		require.ErrorIs(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, invalid)), domain.ErrInvalidWork)
	}
}

// A response is accepted under the same condition as ordinary input; whether
// its interaction is still open is judged at delivery, not at acceptance.
func TestResponseIsAcceptedWithoutConsultingTheInteraction(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	require.NoError(t, sessions.session.ApplyResponseRequirement(1000, true, time.Now()))
	buffer := &commandtest.InputBuffer{}
	inputs := command.NewInputAcceptance(sessions, buffer)
	reply := command.InteractionResponse{InteractionID: "question", Allow: true, UpdatedInputJSON: `{"answers":{"Proceed?":"Yes"}}`}
	require.NoError(t, inputs.AcceptResponse(ctx, sessions.session.ID, reply))
	require.False(t, sessions.session.Waiting())
	require.Equal(t, []command.InteractionResponse{reply}, buffer.Replies)
	require.ErrorIs(t, inputs.AcceptResponse(ctx, sessions.session.ID, command.InteractionResponse{InteractionID: " "}), domain.ErrInvalidWork)
}

// Input sent while a required interaction is unanswered is held behind it by
// the Harness: it is accepted, and the Turn keeps Waiting for the human. The
// Work tells this from its own facts, so a Dispatcher handover cannot fail it.
func TestInputBehindAnUnansweredRequestKeepsTheTurnWaiting(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	waitAt := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, waitAt))
	require.NoError(t, sessions.session.ApplyResponseRequirement(1000, true, waitAt))
	buffer := &commandtest.InputBuffer{}
	inputs := command.NewInputAcceptance(sessions, buffer)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "also check the logs"}
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)))
	require.True(t, sessions.session.Waiting())
	require.Equal(t, waitAt, sessions.session.HarnessProcess.WaitingSince)
	require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs)
}

// Closing fences acceptance: a closing Work buffers nothing, and an input
// whose commit loses to a concurrent Closing is not accepted and is dropped
// with the rest of the buffer.
func TestClosingFencesAcceptanceAndEmptiesTheBuffer(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
	buffer := &commandtest.InputBuffer{}
	racing := &closingRace{feedbackSessionStore: sessions}
	inputs := command.NewInputAcceptance(racing, buffer)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "late"}
	require.ErrorIs(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)), domain.ErrWorkTransition)
	require.Equal(t, domain.WorkClosing, sessions.session.State)
	require.Empty(t, buffer.Inputs, "the input appended before Closing committed is dropped")
	require.Equal(t, 1, buffer.Dropped)

	require.ErrorIs(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, command.ExecutionInput{ID: uuid.NewString(), Text: "later"})), domain.ErrWorkTransition)
	require.ErrorIs(t, inputs.AcceptResponse(ctx, sessions.session.ID, command.InteractionResponse{InteractionID: "late"}), domain.ErrWorkTransition)
	require.Empty(t, buffer.Inputs)
	require.Empty(t, buffer.Replies)
}

// closingRace commits Closing between the hand-off and the acceptance commit.
type closingRace struct {
	*feedbackSessionStore
	raced bool
}

func (r *closingRace) Save(ctx context.Context, work *domain.AgentWork) error {
	if !r.raced {
		r.raced = true
		closing, _ := r.Get(ctx, work.ID)
		closing.State = domain.WorkClosing
		closing.CloseRequestReference = "close"
		r.session = closing
		return domain.ErrConcurrentModification
	}
	return r.feedbackSessionStore.Save(ctx, work)
}

type completionFacts struct {
	continuation query.WorkContinuation
	err          error
}

func (f *completionFacts) GetWorkContinuation(context.Context, string) (query.WorkContinuation, error) {
	return f.continuation, f.err
}
func (*completionFacts) ListPendingContinuations(context.Context, string, int) ([]string, error) {
	return nil, nil
}

// Buffered means accepted: input whose commit loses to a stop stays buffered
// for the next Resume, and the caller and the record say it was accepted.
func TestBufferedInputIsAcceptedWhenItsCommitLosesToAStop(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
	buffer := &commandtest.InputBuffer{}
	racing := &stopRace{feedbackSessionStore: sessions}
	inputs := command.NewInputAcceptance(racing, buffer)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "while stopping"}
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)))
	require.Equal(t, domain.WorkStopping, sessions.session.State)
	require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs)
	require.Zero(t, buffer.Dropped)
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, input)), "a retry of the buffered identity gets the same answer")
	require.Len(t, buffer.Inputs, 1, "the retry appends nothing")
	buffer.Settle(input.ID)
	handOff, err := inputs.AcceptInput(ctx, sessions.session.ID, input)
	require.NoError(t, err, "a retry after it settled gets the same answer")
	require.Equal(t, command.InputAlreadySettled, handOff, "and learns it already settled")
	require.Empty(t, buffer.Inputs, "a settled identity is not appended again")
	later := command.ExecutionInput{ID: uuid.NewString(), Text: "new"}
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, sessions.session.ID, later)), "new input waits for the next start")
	require.Equal(t, []command.ExecutionInput{later}, buffer.Inputs)
	require.Equal(t, domain.WorkStopping, sessions.session.State)
}

// stopRace commits a stop between the hand-off and the acceptance commit.
type stopRace struct {
	*feedbackSessionStore
	raced bool
}

func (r *stopRace) Save(ctx context.Context, work *domain.AgentWork) error {
	if !r.raced {
		r.raced = true
		stopping, _ := r.Get(ctx, work.ID)
		if err := stopping.Stop(ctx, "stop", controlPort{}); err != nil {
			return err
		}
		r.session = stopping
		return domain.ErrConcurrentModification
	}
	return r.feedbackSessionStore.Save(ctx, work)
}

// acceptance is the error of accepting an input, whatever the hand-off.
func acceptance(_ command.InputHandOff, err error) error { return err }
