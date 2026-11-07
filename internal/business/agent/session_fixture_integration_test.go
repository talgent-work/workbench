//go:build integration

package agent_test

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// sessionControlRequest is the context of a call under the control token.
func sessionControlRequest(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer control")
	return ctx
}

type sessionCommandRecorder struct {
	// The Harness settings and live reads it does not record are unused.
	unusedExecutionPort
	inputError, replyError error
	pending                []command.PendingInteraction
	mu                     sync.Mutex
	starts                 []domain.StartExecutionRequest
	stops                  []struct {
		workID    string
		requestID string
	}
	reclaims []string
	// buffered and replies are the input buffer's content by identity, as
	// Dispatcher keeps it.
	buffered map[string]string
	replies  map[string]bool
}

// lifecycleProcesses is the environment authority's controlled observation.
type lifecycleProcesses struct {
	mu          sync.Mutex
	observation domain.ProcessObservation
}

func (p *lifecycleProcesses) observe(observation domain.ProcessObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.observation = observation
}

func (p *lifecycleProcesses) ObserveProcess(context.Context, string) (command.WorkProcessObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return command.WorkProcessObservation{ProcessObservation: p.observation}, nil
}

// The Work's current process is "process" in these fixtures.
func (*lifecycleProcesses) IsCurrentProcess(_ context.Context, _ string, processID string) (bool, error) {
	return processID == "process", nil
}

func (*lifecycleProcesses) AcknowledgeExit(_ context.Context, _ string, processID string) (bool, error) {
	return processID == "process", nil
}

// The recorder stands in for Dispatcher's input buffer: it holds each input
// identity once until the test settles it, and refuses other content under a
// buffered identity.
func (p *sessionCommandRecorder) BufferInput(_ context.Context, _ string, input command.ExecutionInput) (command.InputHandOff, error) {
	if p.inputError != nil {
		return "", p.inputError
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if text, buffered := p.buffered[input.ID]; buffered {
		if text != input.Text {
			return "", command.ErrInputAdmissionConflict
		}
		return command.InputRepeated, nil
	}
	if p.buffered == nil {
		p.buffered = map[string]string{}
	}
	p.buffered[input.ID] = input.Text
	return command.InputBuffered, nil
}

func (p *sessionCommandRecorder) BufferResponse(_ context.Context, _ string, response command.InteractionResponse) (command.InputHandOff, error) {
	if p.replyError != nil {
		return "", p.replyError
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.replies[response.InteractionID] {
		return command.InputRepeated, nil
	}
	if p.replies == nil {
		p.replies = map[string]bool{}
	}
	p.replies[response.InteractionID] = true
	return command.InputBuffered, nil
}

func (p *sessionCommandRecorder) ReadBuffer(context.Context, string) (command.BufferedInputs, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result command.BufferedInputs
	for id := range p.buffered {
		result.InputIDs = append(result.InputIDs, id)
	}
	for id := range p.replies {
		result.RepliedInteractionIDs = append(result.RepliedInteractionIDs, id)
	}
	return result, nil
}

func (p *sessionCommandRecorder) DropBuffer(context.Context, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buffered, p.replies = nil, nil
	return nil
}

// settle removes an input as Dispatcher does once the Harness settles it.
func (p *sessionCommandRecorder) settle(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.buffered, id)
	delete(p.replies, id)
}

func (p *sessionCommandRecorder) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, request)
	return nil
}

func (p *sessionCommandRecorder) startRequests() []domain.StartExecutionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.StartExecutionRequest(nil), p.starts...)
}

// distinctStartRequests collapses redelivery of the same start. Recovery
// re-sends the start of a Work still Starting (at-least-once delivery), and the
// production port registers one allocation per Work and request fingerprint,
// so only distinct starts are execution admissions.
func (p *sessionCommandRecorder) distinctStartRequests() []domain.StartExecutionRequest {
	var distinct []domain.StartExecutionRequest
	for _, request := range p.startRequests() {
		if !slices.ContainsFunc(distinct, func(seen domain.StartExecutionRequest) bool { return reflect.DeepEqual(seen, request) }) {
			distinct = append(distinct, request)
		}
	}
	return distinct
}

func (p *sessionCommandRecorder) StopExecution(_ context.Context, workID string, requestID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops = append(p.stops, struct {
		workID    string
		requestID string
	}{workID, requestID})
	return nil
}
func (*sessionCommandRecorder) RetireFencedRun(context.Context, string) error { return nil }

func (p *sessionCommandRecorder) ReclaimCompute(_ context.Context, workID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reclaims = append(p.reclaims, workID)
	return nil
}

func (p *sessionCommandRecorder) assertStop(t *testing.T, workID string, requestID string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.NotEmpty(t, p.stops)
	last := p.stops[len(p.stops)-1]
	require.Equal(t, workID, last.workID)
	require.Equal(t, requestID, last.requestID)
}

func (p *sessionCommandRecorder) assertReclaim(t *testing.T, workID string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.NotEmpty(t, p.reclaims)
	require.Equal(t, workID, p.reclaims[len(p.reclaims)-1])
}

func (p *sessionCommandRecorder) ListPendingInteractions(context.Context, string) ([]command.PendingInteraction, error) {
	return append([]command.PendingInteraction(nil), p.pending...), nil
}

func (*sessionCommandRecorder) CancelRunningTurn(context.Context, string) error { return nil }
