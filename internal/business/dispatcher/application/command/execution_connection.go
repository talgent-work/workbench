package command

import (
	"context"
	"errors"
)

var (
	ErrExecutionConnection         = errors.New("dispatcher: invalid execution connection")
	ErrExecutionConnectionConflict = errors.New("dispatcher: execution connection conflicts with its current owner")
	ErrExecutionNotReady           = errors.New("dispatcher: execution is not ready for this request")
	// ErrTurnActive refuses, retryably, to stop a process during a Turn: a
	// process is stopped only between Turns.
	ErrTurnActive = errors.New("dispatcher: a Turn is active")
)

// A Work has at most one Harness process, so the Work addresses it. ProcessID
// is the execd handle of the process launched in the Work's current sandbox.
type ConnectExecution struct {
	WorkID                                            string
	Endpoint, AccessToken, ProcessID, NativeSessionID string
}

type StopExecution struct {
	WorkID        string
	StopRequestID string
}

// ExecutionConnectionPort controls the native process already allocated for an
// admitted Work start. Work admission and business state remain in Agent.
type ExecutionConnectionPort interface {
	Connect(context.Context, ConnectExecution) error
	Stop(context.Context, StopExecution) error
}

type ConnectExecutionHandler struct{ connections ExecutionConnectionPort }

func NewConnectExecutionHandler(connections ExecutionConnectionPort) *ConnectExecutionHandler {
	return &ConnectExecutionHandler{connections}
}
func (h *ConnectExecutionHandler) Handle(ctx context.Context, input ConnectExecution) error {
	return h.connections.Connect(ctx, input)
}

type StopExecutionHandler struct{ connections ExecutionConnectionPort }

func NewStopExecutionHandler(connections ExecutionConnectionPort) *StopExecutionHandler {
	return &StopExecutionHandler{connections}
}
func (h *StopExecutionHandler) Handle(ctx context.Context, input StopExecution) error {
	return h.connections.Stop(ctx, input)
}
