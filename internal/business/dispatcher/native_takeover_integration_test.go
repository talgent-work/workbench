//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"

	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

// fakeExecd is one Harness process's retained output, readable again from any
// earlier position, as execd keeps a Harness Output Stream.
type fakeExecd struct {
	mu       sync.Mutex
	output   []byte
	exitCode *int
	changed  chan struct{}
	attaches chan int64
	inputs   chan []byte
	server   *httptest.Server
}

func newFakeExecd(t *testing.T) *fakeExecd {
	t.Helper()
	e := &fakeExecd{changed: make(chan struct{}), attaches: make(chan int64, 16), inputs: make(chan []byte, 64)}
	e.server = httptest.NewServer(websocket.Handler(e.serve))
	t.Cleanup(e.server.Close)
	return e
}

func (e *fakeExecd) write(lines ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, line := range lines {
		e.output = append(e.output, line+"\n"...)
	}
	close(e.changed)
	e.changed = make(chan struct{})
}

func (e *fakeExecd) exit(code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exitCode = &code
	close(e.changed)
	e.changed = make(chan struct{})
}

func (e *fakeExecd) serve(ws *websocket.Conn) {
	defer ws.Close()
	since, _ := strconv.ParseInt(ws.Request().URL.Query().Get("since"), 10, 64)
	e.attaches <- since
	go func() {
		for {
			var input []byte
			if websocket.Message.Receive(ws, &input) != nil {
				return
			}
			e.inputs <- input
		}
	}()
	e.mu.Lock()
	sent := int64(len(e.output))
	replay := make([]byte, 9, 9+len(e.output))
	replay[0] = 3
	binary.BigEndian.PutUint64(replay[1:], uint64(since))
	replay = append(replay, e.output[since:]...)
	e.mu.Unlock()
	if websocket.Message.Send(ws, replay) != nil || websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
		return
	}
	for {
		e.mu.Lock()
		changed, output, exitCode := e.changed, e.output, e.exitCode
		e.mu.Unlock()
		if int64(len(output)) > sent {
			if websocket.Message.Send(ws, append([]byte{1}, output[sent:]...)) != nil {
				return
			}
			sent = int64(len(output))
			continue
		}
		if exitCode != nil {
			_ = websocket.Message.Send(ws, fmt.Sprintf(`{"type":"exit","exit_code":%d}`, *exitCode))
			return
		}
		<-changed
	}
}

const initialized = `{"type":"control_response","response":{"subtype":"success","request_id":"initialize-process"}}`

func turn(n int) []string {
	return []string{
		fmt.Sprintf(`{"type":"assistant","uuid":"assistant-%d","message":{"content":[{"type":"tool_use","id":"tool-%d","name":"Bash","input":{"command":"echo %d"}}]}}`, n, n, n),
		fmt.Sprintf(`{"type":"user","uuid":"output-%d","message":{"content":[{"type":"tool_result","tool_use_id":"tool-%d","content":"%d"}]}}`, n, n, n),
		fmt.Sprintf(`{"type":"result","subtype":"success","uuid":"result-%d","result":"turn %d"}`, n, n),
	}
}

// resultCount counts distinct stored RESULT identities.
func (r *nativeRecords) resultCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	results := map[string]bool{}
	for _, m := range r.messages {
		if m.GetResult() != nil {
			results[m.GetId()] = true
		}
	}
	return len(results)
}

// hasError reports whether readers were told of any error.
func (r *nativeRecords) hasError() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.messages {
		if m.GetError() != nil {
			return true
		}
	}
	return false
}

func (r *nativeRecords) distinctMessageIDs() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := map[string]bool{}
	for _, m := range r.messages {
		ids[m.GetId()] = true
	}
	return len(ids), len(r.messages)
}

// Intent (real): on SIGTERM a replica hands its stream over at the exact
// position; the peer attaches from there without replay or duplicate messages,
// and serves the Work's commands through the routed private API.
func TestHandoverResumesFromTheExactPositionWithoutDuplicates(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	execd.write(turn(1)...)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	delivery := newTestDelivery(t, records, records)
	a := replicas.replica("a", records, delivery)
	target := command.ConnectExecution{WorkID: "handover-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, a.Connect(ctx, target))
	require.Equal(t, int64(0), <-execd.attaches)
	require.Eventually(t, func() bool { return records.resultCount() == 1 }, 5*time.Second, 10*time.Millisecond)

	b, _ := replicas.servedReplica("b", records, delivery)
	shutdown, done := context.WithTimeout(ctx, 5*time.Second)
	require.NoError(t, a.Shutdown(shutdown))
	done()
	select {
	case offset := <-execd.attaches:
		require.Positive(t, offset, "the peer resumes at the handed-over position, not from the start")
	case <-ctx.Done():
		t.Fatal("no replica took the handed-over stream")
	}
	execd.write(turn(2)...)
	require.Eventually(t, func() bool { return records.resultCount() == 2 }, 5*time.Second, 10*time.Millisecond)
	distinct, total := records.distinctMessageIDs()
	require.Equal(t, distinct, total, "a handover repeats no message")
	_, err := b.GetCurrentTurn(ctx, target.WorkID)
	require.NoError(t, err, "the peer now owns the stream")

	// A command reaching a replica that does not own the stream is forwarded
	// one hop to the owner, which answers ObserveProcess from memory.
	_, ingress := replicas.servedReplica("ingress", records, delivery)
	observed, err := ingress.ObserveProcess(ctx, nativeRequest(&dispatcherprivatev1.ObserveProcessRequest{WorkId: target.WorkID, ProcessId: target.ProcessID}))
	require.NoError(t, err)
	require.Equal(t, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING, observed.Msg.GetState())
	require.True(t, observed.Msg.GetReady())
	_, err = ingress.ObserveProcess(ctx, nativeRequest(&dispatcherprivatev1.ObserveProcessRequest{WorkId: "unowned", ProcessId: "process"}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "without a live owner the answer is retryable")
}

// Intent (real): a replica that stops refreshing its liveness loses its stream
// to a survivor within about TTL plus the scan interval; the survivor replays
// from the snapshot, the repeated messages keep their identities, and the
// expired replica's late snapshot write is fenced by the generation.
func TestSurvivorTakesOverACrashedReplica(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	execd.write(turn(1)...)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	delivery := newTestDelivery(t, records, records)
	crashing := replicas.client()
	a, err := dispatcher.NewNativeExecutions(http.DefaultClient, records, delivery, dispatcherredis.NewOwnership(crashing, "a", "127.0.0.1:0", fastTiming.TTL.Duration(), time.Hour), dispatcherredis.NewInputBuffer(crashing), fastTiming, slog.Default())
	require.NoError(t, err)
	require.NoError(t, a.Start(ctx))
	target := command.ConnectExecution{WorkID: "takeover-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, a.Connect(ctx, target))
	<-execd.attaches
	require.Eventually(t, func() bool { return records.resultCount() == 1 }, 5*time.Second, 10*time.Millisecond)
	owner, _, err := replicas.ownership("observer", "").Owner(ctx, target.WorkID)
	require.NoError(t, err)
	require.Equal(t, "a", owner.Instance)
	// The next turn starts; its frames are read but are not a boundary.
	boundary := int64(len(initialized) + 1)
	for _, line := range turn(1) {
		boundary += int64(len(line) + 1)
	}
	execd.write(turn(2)[0], turn(2)[1])
	require.Eventually(t, func() bool { _, total := records.distinctMessageIDs(); return total >= 5 }, 5*time.Second, 10*time.Millisecond)

	b := replicas.replica("b", records, delivery)
	crashed := time.Now()
	require.NoError(t, crashing.Close(), "replica a can no longer reach Redis")
	var resumedAt int64
	select {
	case resumedAt = <-execd.attaches:
	case <-ctx.Done():
		t.Fatal("no survivor took the stream over")
	}
	require.Less(t, time.Since(crashed), fastTiming.TTL.Duration()+3*fastTiming.Scan.Duration()+2*time.Second, "takeover within about TTL plus a scan")
	require.Equal(t, boundary, resumedAt, "the survivor replays from the last snapshot, the turn-1 result")
	execd.write(turn(2)[2])
	require.Eventually(t, func() bool { return records.resultCount() == 2 }, 5*time.Second, 10*time.Millisecond)
	_, err = b.GetCurrentTurn(ctx, target.WorkID)
	require.NoError(t, err)
	distinct, total := records.distinctMessageIDs()
	require.Equal(t, 6, distinct, "replayed messages keep their identities, so storage keeps one of each")
	require.Greater(t, total, distinct, "the replay repeated the turn-2 frames read before the crash")

	late := replicas.ownership("a", "127.0.0.1:0")
	require.ErrorIs(t, late.SaveSnapshot(ctx, target.WorkID, owner.Generation, []byte(`{}`)), dispatcherredis.ErrFenced, "the expired replica's late write is rejected")
	require.Eventually(t, func() bool {
		_, err := a.GetCurrentTurn(ctx, target.WorkID)
		return errors.Is(err, command.ErrNoActiveConnection)
	}, 5*time.Second, 20*time.Millisecond, "a replica that cannot refresh its liveness stops reading")
}

// exitReporter acknowledges an exit fact only after rejecting it a few times,
// and records when each turn's end is reported against stored RESULTs.
type exitReporter struct {
	*nativeRecords
	rejections       atomic.Int32
	attempts         atomic.Int32
	block            chan struct{}
	resultsAtTurnEnd []int
	mu               sync.Mutex
}

func (r *exitReporter) ReportExecution(ctx context.Context, observation command.ExecutionObservation) error {
	if r.block != nil && observation.Kind == command.ExecutionTurnStarted {
		<-r.block
	}
	if observation.Kind == command.ExecutionTurnFinished {
		r.mu.Lock()
		r.resultsAtTurnEnd = append(r.resultsAtTurnEnd, r.nativeRecords.resultCount())
		r.mu.Unlock()
	}
	if observation.Kind == command.ExecutionExited {
		r.attempts.Add(1)
		if r.rejections.Add(-1) >= 0 {
			return command.ErrExecutionFeedbackUnavailable
		}
	}
	return r.nativeRecords.ReportExecution(ctx, observation)
}

// Intent (real): a RESULT is stored before its turn is reported finished; the
// exit fact is redelivered until Agent acknowledges it, after which the stream
// is forgotten; a blocked feedback queue never stops reading.
func TestExitFactIsRedeliveredAndResultsPrecedeTurnEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	reporter := &exitReporter{nativeRecords: records, block: make(chan struct{})}
	reporter.rejections.Store(2)
	manager := replicas.replica("a", reporter, newTestDelivery(t, records, records))
	target := command.ConnectExecution{WorkID: "exit-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, manager.Connect(ctx, target))

	// The first turn's start blocks feedback; reading continues meanwhile.
	execd.write(turn(1)...)
	execd.write(turn(2)...)
	require.Eventually(t, func() bool {
		observation, err := manager.ObserveProcess(ctx, target.WorkID, target.ProcessID)
		return err == nil && observation.Turn.Completed && observation.Turn.Sequence > 0 && records.resultCount() == 2
	}, 5*time.Second, 10*time.Millisecond, "a blocked feedback queue does not stop reading")
	close(reporter.block)
	require.Eventually(t, func() bool {
		reporter.mu.Lock()
		defer reporter.mu.Unlock()
		return len(reporter.resultsAtTurnEnd) == 2
	}, 5*time.Second, 10*time.Millisecond)
	reporter.mu.Lock()
	require.GreaterOrEqual(t, reporter.resultsAtTurnEnd[0], 1, "the first RESULT is stored before its turn end is reported")
	require.Equal(t, 2, reporter.resultsAtTurnEnd[1])
	reporter.mu.Unlock()

	execd.exit(0)
	require.Eventually(t, func() bool {
		observation, err := manager.ObserveProcess(ctx, target.WorkID, target.ProcessID)
		return err == nil && observation.State == application.ProcessPending
	}, 5*time.Second, 10*time.Millisecond, "an exited process is not reported running while its exit awaits acknowledgement")
	require.Eventually(t, func() bool { return reporter.attempts.Load() == 3 }, 10*time.Second, 20*time.Millisecond, "the exit fact is redelivered until acknowledged")
	exit := records.awaitWork(t, target.WorkID, command.ExecutionExited)
	require.Equal(t, target.ProcessID, exit.ProcessID)
	require.Empty(t, exit.ExitReason, "whether the exit was normal is Agent's decision")
	require.False(t, records.hasError(), "an exit is the Work's fact, not a conversation message")
	require.Eventually(t, func() bool {
		_, owned, err := replicas.ownership("observer", "").Owner(ctx, target.WorkID)
		return err == nil && !owned
	}, 5*time.Second, 20*time.Millisecond, "an acknowledged exit releases the stream")
	_, snapshot, err := replicas.ownership("observer", "").Snapshot(ctx, target.WorkID)
	require.NoError(t, err)
	require.True(t, snapshot, "exit acknowledgement releases ownership but retains Session state for Resume")

	records.mu.Lock()
	defer records.mu.Unlock()
	for _, message := range records.messages {
		if message.GetResult() != nil {
			require.Equal(t, dispatcherpublicv1.ResultStatus_RESULT_STATUS_SUCCESS, message.GetResult().GetStatus())
		}
	}
}
