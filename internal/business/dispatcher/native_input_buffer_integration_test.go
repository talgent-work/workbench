//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// written is one frame Dispatcher wrote to the Harness's stdin.
type written struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	Signal    string `json:"signal"`
	RequestID string `json:"request_id"`
	Response  struct {
		RequestID string `json:"request_id"`
	} `json:"response"`
}

// nextWrite waits for the next frame written to the fake process. Input
// frames carry a one-byte pipe type before their JSON.
func nextWrite(t *testing.T, ctx context.Context, e *fakeExecd) written {
	t.Helper()
	select {
	case raw := <-e.inputs:
		var frame written
		if len(raw) > 0 && raw[0] != '{' {
			raw = raw[1:]
		}
		require.NoError(t, json.Unmarshal(raw, &frame), string(raw))
		return frame
	case <-ctx.Done():
		t.Fatal("no frame was written to the Harness")
		return written{}
	}
}

func noWrite(t *testing.T, e *fakeExecd, wait time.Duration) {
	t.Helper()
	select {
	case raw := <-e.inputs:
		t.Fatalf("unexpected write to the Harness: %s", raw)
	case <-time.After(wait):
	}
}

func (r *nativeRecords) settled(workID, inputID string) (command.InputOutcome, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, observation := range r.observations {
		if observation.WorkID == workID && observation.Kind == command.ExecutionInputSettled && observation.InputID == inputID {
			return observation.InputOutcome, true
		}
	}
	return "", false
}

func (r *nativeRecords) awaitSettled(t *testing.T, workID, inputID string, outcome command.InputOutcome) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, ok := r.settled(workID, inputID)
		return ok && got == outcome
	}, 5*time.Second, 10*time.Millisecond, "input %s settles %s", inputID, outcome)
}

func lifecycle(id, state string) string {
	return fmt.Sprintf(`{"type":"command_lifecycle","command_uuid":"%s","state":"%s"}`, id, state)
}

// Intent (real): entries appended to a Work's buffer, from any replica, are
// written to the Harness in admission order without waiting on each other;
// each settles by its own disposition, a reply to an interaction that does
// not exist settles closed, and a process stops only between Turns.
func TestBufferedInputIsWrittenInOrderAndSettlesIndividually(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	owner := replicas.replica("owner", records, newTestDelivery(t, records, records))
	buffer := replicas.inputs()
	target := command.ConnectExecution{WorkID: "buffer-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, owner.Connect(ctx, target))
	<-execd.attaches
	records.awaitWork(t, target.WorkID, command.ExecutionStarted)

	first := command.BufferedInput{ID: uuid.NewString(), Text: "first"}
	second := command.BufferedInput{ID: uuid.NewString(), Text: "second"}
	for _, input := range []command.BufferedInput{first, second} {
		handOff, err := buffer.Append(ctx, target.WorkID, input)
		require.NoError(t, err)
		require.Equal(t, command.InputBuffered, handOff)
	}
	require.Equal(t, written{Type: "user", UUID: first.ID}, nextWrite(t, ctx, execd))
	require.Equal(t, written{Type: "user", UUID: second.ID}, nextWrite(t, ctx, execd), "the second input does not wait for the first to be processed")

	execd.write(lifecycle(first.ID, "started"), lifecycle(first.ID, "completed"))
	records.awaitSettled(t, target.WorkID, first.ID, command.InputCompleted)
	remaining, err := buffer.List(ctx, target.WorkID)
	require.NoError(t, err)
	require.Equal(t, []command.BufferedInput{second}, remaining, "a completed input settles only itself")
	handOff, err := buffer.Append(ctx, target.WorkID, first)
	require.NoError(t, err)
	require.Equal(t, command.InputSettled, handOff)
	noWrite(t, execd, 200*time.Millisecond)

	ghost := command.BufferedInput{ID: "no-such-request", Reply: &command.BufferedReply{Allow: true}}
	_, err = buffer.Append(ctx, target.WorkID, ghost)
	require.NoError(t, err)
	records.awaitSettled(t, target.WorkID, ghost.ID, command.InputClosed)
	noWrite(t, execd, 200*time.Millisecond)
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, notice := range records.notices {
			if notice.AgentWorkID == target.WorkID && notice.Reason == "interaction_closed" && notice.Severity == "warning" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "readers are told the reply was not used")

	require.ErrorIs(t, owner.Stop(ctx, command.StopExecution{WorkID: target.WorkID, StopRequestID: "stop"}), command.ErrTurnActive, "the second input keeps a Turn active")
	execd.write(lifecycle(second.ID, "completed"), `{"type":"result","subtype":"success","uuid":"result-1","result":"done"}`)
	records.awaitSettled(t, target.WorkID, second.ID, command.InputCompleted)
	require.Eventually(t, func() bool {
		return owner.Stop(ctx, command.StopExecution{WorkID: target.WorkID, StopRequestID: "stop"}) == nil
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, "SIGINT", nextWrite(t, ctx, execd).Signal)
	execd.exit(0)
	exit := records.awaitWork(t, target.WorkID, command.ExecutionExited)
	require.Zero(t, exit.ExitCode)
	require.False(t, records.hasError(), "an exit Dispatcher asked for is not an error")
}

// Intent (real): a takeover writes the written entries again, which the
// Harness deduplicates; input buffered while a Turn cancellation is pending
// waits for it; at exit written but unfinished input is abandoned and
// unwritten input waits for the next process.
func TestTakeoverRewritesWrittenInputAndExitAbandonsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	delivery := newTestDelivery(t, records, records)
	a := replicas.replica("a", records, delivery)
	buffer := replicas.inputs()
	target := command.ConnectExecution{WorkID: "rewrite-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, a.Connect(ctx, target))
	<-execd.attaches
	records.awaitWork(t, target.WorkID, command.ExecutionStarted)
	written := command.BufferedInput{ID: uuid.NewString(), Text: "written before the handover"}
	_, err := buffer.Append(ctx, target.WorkID, written)
	require.NoError(t, err)
	require.Equal(t, written.ID, nextWrite(t, ctx, execd).UUID)

	b, _ := replicas.servedReplica("b", records, delivery)
	shutdown, done := context.WithTimeout(ctx, 5*time.Second)
	require.NoError(t, a.Shutdown(shutdown))
	done()
	<-execd.attaches
	require.Equal(t, written.ID, nextWrite(t, ctx, execd).UUID, "the new reader writes the unsettled entry again")

	execd.write(`{"type":"assistant","uuid":"assistant-1","message":{"content":[{"type":"text","text":"working"}]}}`)
	var turnID string
	require.Eventually(t, func() bool {
		turn, err := b.GetCurrentTurn(ctx, target.WorkID)
		turnID = turn.ID
		return err == nil && turn.Active && turn.ID != ""
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, b.CancelTurn(ctx, application.CancelTurn{WorkID: target.WorkID, TurnID: turnID}))
	require.Equal(t, "talgent-cancel-"+turnID, nextWrite(t, ctx, execd).RequestID)
	queued := command.BufferedInput{ID: uuid.NewString(), Text: "buffered during a cancellation"}
	_, err = buffer.Append(ctx, target.WorkID, queued)
	require.NoError(t, err)
	noWrite(t, execd, 300*time.Millisecond)

	execd.exit(0)
	records.awaitWork(t, target.WorkID, command.ExecutionExited)
	records.awaitSettled(t, target.WorkID, written.ID, command.InputAbandoned)
	remaining, err := buffer.List(ctx, target.WorkID)
	require.NoError(t, err)
	require.Equal(t, []command.BufferedInput{queued}, remaining, "unwritten input waits for the next process")
}

// Intent (real): when a handover lands between the Harness ending a cancelled
// Turn and settling its input, the new reader rewrites the unsettled input,
// which the Harness deduplicates. The rewrite starts no Turn, so the Work stays
// between Turns once the input settles.
func TestRewrittenInputDoesNotReviveAnEndedTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	delivery := newTestDelivery(t, records, records)
	a := replicas.replica("a", records, delivery)
	buffer := replicas.inputs()
	target := command.ConnectExecution{WorkID: "ended-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, a.Connect(ctx, target))
	<-execd.attaches
	records.awaitWork(t, target.WorkID, command.ExecutionStarted)
	input := command.BufferedInput{ID: uuid.NewString(), Text: "cancel this question"}
	_, err := buffer.Append(ctx, target.WorkID, input)
	require.NoError(t, err)
	require.Equal(t, input.ID, nextWrite(t, ctx, execd).UUID)
	execd.write(lifecycle(input.ID, "started"), `{"type":"assistant","uuid":"assistant-1","message":{"content":[{"type":"text","text":"working"}]}}`)
	var turnID string
	require.Eventually(t, func() bool {
		turn, err := a.GetCurrentTurn(ctx, target.WorkID)
		turnID = turn.ID
		return err == nil && turn.Active && turn.ID != ""
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, a.CancelTurn(ctx, application.CancelTurn{WorkID: target.WorkID, TurnID: turnID}))
	require.Equal(t, "talgent-cancel-"+turnID, nextWrite(t, ctx, execd).RequestID)
	// The Harness ends the interrupted Turn; its input's terminal disposition
	// follows the RESULT and arrives only after the handover.
	execd.write(`{"type":"control_response","response":{"subtype":"success","request_id":"talgent-cancel-`+turnID+`"}}`,
		`{"type":"result","subtype":"error_during_execution","uuid":"result-1","is_error":true}`)
	records.awaitWork(t, target.WorkID, command.ExecutionTurnFinished)

	b, _ := replicas.servedReplica("b", records, delivery)
	shutdown, done := context.WithTimeout(ctx, 5*time.Second)
	require.NoError(t, a.Shutdown(shutdown))
	done()
	<-execd.attaches
	require.Equal(t, input.ID, nextWrite(t, ctx, execd).UUID, "the new reader writes the unsettled entry again")
	execd.write(lifecycle(input.ID, "cancelled"))
	records.awaitSettled(t, target.WorkID, input.ID, command.InputCompleted)
	turn, err := b.GetCurrentTurn(ctx, target.WorkID)
	require.NoError(t, err)
	require.False(t, turn.Active, "a deduplicated rewrite starts no Turn")
	require.NoError(t, b.Stop(ctx, command.StopExecution{WorkID: target.WorkID, StopRequestID: "stop"}), "the Work is between Turns")
}
