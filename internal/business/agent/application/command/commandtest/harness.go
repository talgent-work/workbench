package commandtest

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// Harness stands in for the running Harness of one Work beyond its input
// buffer: it records exit signals and Turn cancellations, and answers a
// test's current Turn and process observation.
type Harness struct {
	*InputBuffer
	mu sync.Mutex
	// Turn is the Turn the Harness runs now.
	Turn command.CurrentTurn
	// Observation is Dispatcher's observation of the launched process.
	Observation command.WorkProcessObservation
	ObserveErr  error
	StopErr     error
	// Stops are the stop identities signalled; Cancels the Turns cancelled;
	// Observed the processes observed.
	Stops, Cancels, Observed []string
}

var _ command.DispatcherPort = (*Harness)(nil)

// NewHarness is a Harness over an empty input buffer.
func NewHarness() *Harness { return &Harness{InputBuffer: &InputBuffer{}} }

func (h *Harness) StopExecution(_ context.Context, _ string, stopRequestID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Stops = append(h.Stops, stopRequestID)
	return h.StopErr
}

func (h *Harness) GetCurrentTurn(context.Context, string) (command.CurrentTurn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Turn, nil
}

func (h *Harness) CancelTurn(_ context.Context, _ string, turnID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Cancels = append(h.Cancels, turnID)
	h.Turn.CancelRequested = true
	return nil
}

func (h *Harness) ObserveProcess(_ context.Context, _ string, processID string) (command.WorkProcessObservation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Observed = append(h.Observed, processID)
	return h.Observation, h.ObserveErr
}
