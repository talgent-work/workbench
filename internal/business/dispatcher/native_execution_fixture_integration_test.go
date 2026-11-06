//go:build integration

package dispatcher_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	"google.golang.org/protobuf/proto"
)

func nativeRequest[T any](message *T) *connect.Request[T] {
	r := connect.NewRequest(message)
	r.Header().Set("Authorization", "Bearer control-test")
	return r
}

type nativeRecords struct {
	rejectPlan, lostPlan  bool
	rejectInputCompletion bool
	notices               []messagedelivery.Diagnostic
	lostInputCompletion   bool
	mu                    sync.Mutex
	observations          []command.ExecutionObservation
	messages              []*dispatcherpublicv1.UnifiedMessage
	rejectExit            bool
	lostExit              bool
	liveUnavailable       bool
	runs                  map[string]int
	processes             map[string]string
	endedConnections      []string
}

func (r *nativeRecords) ReportConnectionEnded(_ context.Context, workID, processID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endedConnections = append(r.endedConnections, workID+"/"+processID)
	return nil
}

func (r *nativeRecords) connectionsEnded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.endedConnections...)
}

func (r *nativeRecords) ReportExecution(_ context.Context, o command.ExecutionObservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if o.Kind == command.ExecutionWorkProgressUpdated && r.rejectPlan {
		r.lostPlan = true
		return errors.New("injected plan feedback loss")
	}

	if o.Kind == command.ExecutionExited && r.rejectExit {
		r.lostExit = true
		return errors.New("injected exit feedback failure")
	}
	if o.Kind == command.ExecutionInputSettled && r.rejectInputCompletion {
		r.lostInputCompletion = true
		return errors.New("injected input completion feedback loss")
	}
	r.observations = append(r.observations, o)
	return nil
}
func (r *nativeRecords) Append(_ context.Context, messages ...*dispatcherpublicv1.UnifiedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range messages {
		r.messages = append(r.messages, proto.CloneOf(m))
	}
	return nil
}
func (r *nativeRecords) PublishDiagnostic(_ context.Context, diagnostic messagedelivery.Diagnostic) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, diagnostic)
	return nil
}
func (r *nativeRecords) PublishLive(context.Context, *dispatcherpublicv1.UnifiedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.liveUnavailable {
		return errors.New("live publisher unavailable")
	}
	return nil
}

// newTestDelivery delivers into records, publishing live through publisher.
func newTestDelivery(t *testing.T, records *nativeRecords, publisher messagedelivery.LivePublisher) *messagedelivery.Sink {
	t.Helper()
	delivery, err := messagedelivery.NewSink(records, publisher, records, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = delivery.Close(context.Background()) })
	return delivery
}

// mark records where a labeled run of a process begins; the run's feedback is
// that process's from then on, apart from other processes still ending.
func (r *nativeRecords) mark(run, processID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs, r.processes = map[string]int{}, map[string]string{}
	}
	r.runs[run], r.processes[run] = len(r.observations), processID
}

func (r *nativeRecords) await(t *testing.T, execution string, kind command.ExecutionObservationKind) command.ExecutionObservation {
	t.Helper()
	var found command.ExecutionObservation
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, o := range r.observations[r.runs[execution]:] {
			if o.Kind == kind && o.ProcessID == r.processes[execution] {
				found = o
				return true
			}
		}
		return false
	}, 15*time.Second, 10*time.Millisecond, "execution %s missing %s", execution, kind)
	return found
}

// awaitExited returns a run's exit fact. When the exit races an input write,
// execd fails the write and ends the stream without the exit frame; Dispatcher
// reports the ended connection and Agent connects again, which reconnect does
// here. The new attachment delivers the exit.
func (r *nativeRecords) awaitExited(t *testing.T, execution, workID string, reconnect func()) command.ExecutionObservation {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	handled := 0
	for {
		r.mu.Lock()
		for _, o := range r.observations[r.runs[execution]:] {
			if o.Kind == command.ExecutionExited && o.ProcessID == r.processes[execution] {
				r.mu.Unlock()
				return o
			}
		}
		ended := 0
		for _, connection := range r.endedConnections {
			if connection == workID+"/"+r.processes[execution] {
				ended++
			}
		}
		r.mu.Unlock()
		if ended > handled {
			handled = ended
			reconnect()
		}
		require.True(t, time.Now().Before(deadline), "execution %s missing %s", execution, command.ExecutionExited)
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *nativeRecords) awaitWork(t *testing.T, workID string, kind command.ExecutionObservationKind) command.ExecutionObservation {
	t.Helper()
	var found command.ExecutionObservation
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, o := range r.observations {
			if o.WorkID == workID && o.Kind == kind {
				found = o
				return true
			}
		}
		return false
	}, 15*time.Second, 10*time.Millisecond, "Work %s missing %s", workID, kind)
	return found
}

func (r *nativeRecords) awaitResult(t *testing.T, inputText string) {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		var turn string
		for _, m := range r.messages {
			if m.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER {
				for _, block := range m.GetMessage().Content {
					if block.Text == inputText {
						turn = m.TurnId
					}
				}
			}
		}
		if turn == "" {
			return false
		}
		for _, m := range r.messages {
			if m.TurnId == turn && m.GetResult() != nil {
				return true
			}
		}
		return false
	}, 15*time.Second, 10*time.Millisecond, "input %s missing projected turn result", inputText)
}

// Native stdout is projected by the real adapter. RPC acceptance and tool results
// cannot satisfy this assertion, including after checkpoint recovery above.
func (r *nativeRecords) assertQuestion(t *testing.T, requestID string, phase dispatcherpublicv1.InteractionPhase, answers string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var request, outcome *dispatcherpublicv1.UnifiedMessage
	for _, message := range r.messages {
		if question := message.GetInteraction(); question != nil && question.InteractionId == requestID {
			if question.Phase == dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_REQUESTED {
				request = message
			} else {
				require.Nil(t, outcome, "one native question outcome")
				outcome = message
			}
		}
	}
	require.NotNil(t, request)
	require.NotNil(t, outcome)
	require.NotEmpty(t, request.TurnId)
	require.Equal(t, request.TurnId, outcome.TurnId, "answer stays in the original turn")
	require.Equal(t, request.Agent, outcome.Agent)
	require.NotEmpty(t, request.GetInteraction().ToolCallId)
	require.Equal(t, request.GetInteraction().ToolCallId, outcome.GetInteraction().ToolCallId)
	require.Equal(t, phase, outcome.GetInteraction().Phase)
	require.Positive(t, request.GetInteraction().QuestionCount)
	require.Equal(t, request.GetInteraction().QuestionCount, outcome.GetInteraction().QuestionCount)
	if answers == "" {
		require.Empty(t, outcome.GetInteraction().ResponseJson)
	} else {
		require.JSONEq(t, answers, outcome.GetInteraction().ResponseJson)
	}
}

type nativeInference struct {
	apiFailure   bool
	taskNext     string
	taskTarget   string
	mu           sync.Mutex
	requests     []string
	questionNext bool
	bashNext     string
}

// opensTurn reports whether an inference request's last message carries new
// input rather than only tool results; the Harness may put input after the
// results of an interrupted tool in one message.
func opensTurn(raw []byte) bool {
	var request struct {
		Messages []struct {
			Content json.RawMessage
		}
	}
	if json.Unmarshal(raw, &request) != nil || len(request.Messages) == 0 {
		return true
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &blocks) != nil {
		return true
	}
	for _, block := range blocks {
		if block.Type != "tool_result" && !strings.HasPrefix(block.Text, "<system-reminder>") {
			return true
		}
	}
	return false
}

func (m *nativeInference) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "gzip", 400)
			return
		}
		defer gz.Close()
		body = gz
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "body", 400)
		return
	}
	if strings.Contains(r.URL.Path, "count_tokens") {
		_, _ = io.WriteString(w, `{"input_tokens":10}`)
		return
	}
	if !strings.Contains(r.URL.Path, "/messages") {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	if m.apiFailure {
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"fixture API credential rejected"},"request_id":"fixture-request"}`)
		return
	}
	question := len(m.requests) == 0
	var bash, task, taskTarget string
	// Scripted actions answer the request that opens a Turn. A continuation
	// after a tool result, possibly from another Work's Harness sharing this
	// model, must not take an action meant for the next input.
	if opensTurn(raw) {
		question = question || m.questionNext
		bash = m.bashNext
		task, taskTarget = m.taskNext, m.taskTarget
		m.taskNext, m.taskTarget = "", ""
		m.bashNext = ""
		m.questionNext = false
	}
	m.requests = append(m.requests, string(raw))
	number := len(m.requests)
	m.mu.Unlock()
	block := map[string]any{"type": "text", "text": ""}
	delta := map[string]any{"type": "text_delta", "text": "done"}
	reason := "end_turn"
	if question {
		block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("question-%d", number), "name": "AskUserQuestion", "input": map[string]any{}}
		delta = map[string]any{"type": "input_json_delta", "partial_json": `{"questions":[{"question":"Proceed?","header":"Choice","options":[{"label":"Yes","description":"Proceed"},{"label":"No","description":"Stop"}],"multiSelect":false}]}`}
		reason = "tool_use"
	}
	if bash != "" {
		block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("bash-%d", number), "name": "Bash", "input": map[string]any{}}
		input, _ := json.Marshal(map[string]any{"command": bash, "timeout": 120000, "description": "Run the interruption fixture"})
		delta = map[string]any{"type": "input_json_delta", "partial_json": string(input)}
		reason = "tool_use"
	}
	if task != "" {
		block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("plan-%d", number), "name": task, "input": map[string]any{}}
		input := `{"subject":"Verify native Work progress","description":"Confirm authoritative Work progress","activeForm":"Verifying native Work progress"}`
		if task == "Agent" {
			input = `{"description":"Check native attribution","prompt":"Return a short confirmation.","subagent_type":"general-purpose"}`
		}
		if task == "SendMessage" {
			body, _ := json.Marshal(map[string]string{"to": taskTarget, "message": "Return a follow-up confirmation without tools.", "summary": "Verify member follow-up"})
			input = string(body)
		}
		if task == "TaskUpdate" {
			input = `{"taskId":"1","status":"completed"}`
		}
		if task == "Skill" {
			body, _ := json.Marshal(map[string]string{"skill": taskTarget})
			input = string(body)
		}
		delta = map[string]any{"type": "input_json_delta", "partial_json": input}
		reason = "tool_use"
	}
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_%d", number), "type": "message", "role": "assistant", "content": []any{}, "model": "claude-sonnet-4-6", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": block}, {"type": "content_block_delta", "index": 0, "delta": delta}, {"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 5}}, {"type": "message_stop"},
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		data, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		w.(http.Flusher).Flush()
	}
}
