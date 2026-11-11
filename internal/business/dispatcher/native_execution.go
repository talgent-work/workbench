package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

var (
	// errHandover ends reading because this replica hands the stream to a peer.
	errHandover = errors.New("dispatcher: stream handed over")
	// errFenced ends reading because this replica no longer owns the stream.
	errFenced = errors.New("dispatcher: stream ownership lost")
)

// NativeExecutions reads the Harness Output Stream of each Work this replica
// owns, folding it in memory, and writes the Work's buffered input to it.
// Replicas hand streams over and take over expired peers through Redis without
// Agent. AgentWork owns admission, stop requests and the interpretation of
// feedback.
type NativeExecutions struct {
	client    *http.Client
	feedback  command.ExecutionFeedbackReporter
	delivery  *messagedelivery.Sink
	ownership *dispatcherredis.Ownership
	buffer    *dispatcherredis.InputBuffer
	timing    dispatcherredis.OwnershipConfig
	logger    *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	// connections holds every Work this replica reads or is claiming.
	connections map[string]*nativeExecution
	workers     sync.WaitGroup
	loops       sync.WaitGroup
	// alive is when this replica last refreshed its liveness, in Unix nanoseconds.
	alive atomic.Int64
}

type nativeExecution struct {
	target command.ConnectExecution
	gen    int64
	mu     sync.Mutex
	*nativeprocess.ProtocolState
	connection *nativeprocess.Connection
	attached   bool
	broken     bool
	// buffered is the Work's input buffer as last read, in order.
	buffered []dispatcherredis.InputEntry
	// written holds the entries written on the current connection; a new
	// connection writes every buffered entry again.
	written map[string]bool
	// flushDue marks buffered input that could not be written yet.
	flushDue bool
	// flushFailures counts the flushes in a row that could not read the
	// buffer and retried themselves.
	flushFailures int
	// flushQueued coalesces buffer notifications into one pending flush.
	flushQueued atomic.Bool
	// unsettled holds settlements whose removal from the buffer failed.
	unsettled map[string]settlement
	// exitSignalled marks that this replica asked the process to exit, so the
	// exit is not told to readers as an error.
	exitSignalled bool
	// activityReportedAt is when Turn activity was last reported to Agent.
	activityReportedAt time.Time
	// dirty marks state changed since the last snapshot.
	dirty    bool
	cancel   context.CancelCauseFunc
	settings map[string]chan settingResult
	feedback *feedbackQueue
	done     chan struct{}
	// claimed closes when the claim settles; claimErr is its outcome.
	claimed  chan struct{}
	claimErr error
}

var _ command.ExecutionConnectionPort = (*NativeExecutions)(nil)
var _ application.ProcessObservationReader = (*NativeExecutions)(nil)

func NewNativeExecutions(client *http.Client, feedback command.ExecutionFeedbackReporter, delivery *messagedelivery.Sink, ownership *dispatcherredis.Ownership, buffer *dispatcherredis.InputBuffer, timing dispatcherredis.OwnershipConfig, logger *slog.Logger) (*NativeExecutions, error) {
	if client == nil || feedback == nil || delivery == nil || ownership == nil || buffer == nil || logger == nil {
		return nil, errors.New("dispatcher: native execution dependencies required")
	}
	if err := timing.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &NativeExecutions{client: client, feedback: feedback, delivery: delivery, ownership: ownership, buffer: buffer, timing: timing,
		logger: logger, ctx: ctx, cancel: cancel, connections: make(map[string]*nativeExecution)}, nil
}

// Connect starts reading a process Agent launched. It is idempotent for the
// process this replica already reads.
func (m *NativeExecutions) Connect(ctx context.Context, input command.ConnectExecution) error {
	if strings.TrimSpace(input.WorkID) == "" || strings.TrimSpace(input.ProcessID) == "" {
		return command.ErrExecutionConnection
	}
	if _, err := uuid.Parse(input.NativeSessionID); err != nil {
		return command.ErrExecutionConnection
	}
	if _, err := execd.NewClient(m.client, input.Endpoint, input.AccessToken); err != nil {
		return errors.Join(command.ErrExecutionConnection, err)
	}
	return m.adopt(ctx, input.WorkID, &input)
}

// adopt claims a Work's stream and starts reading it from its snapshot. A nil
// binding takes over with the binding stored in Redis.
func (m *NativeExecutions) adopt(ctx context.Context, workID string, binding *command.ConnectExecution) error {
	entry := &nativeExecution{done: make(chan struct{}), claimed: make(chan struct{})}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return command.ErrExecutionNotReady
	}
	if existing, ok := m.connections[workID]; ok {
		m.mu.Unlock()
		// A Connect racing a claim in flight shares its outcome.
		select {
		case <-existing.claimed:
		case <-ctx.Done():
			return ctx.Err()
		}
		if existing.claimErr != nil {
			return existing.claimErr
		}
		existing.mu.Lock()
		target := existing.target
		existing.mu.Unlock()
		if binding != nil && (target.ProcessID != binding.ProcessID || target.NativeSessionID != binding.NativeSessionID) {
			return command.ErrExecutionConnectionConflict
		}
		return nil
	}
	m.connections[workID] = entry
	m.workers.Add(1)
	m.mu.Unlock()
	client, err := m.claim(ctx, workID, binding, entry)
	if errors.Is(err, dispatcherredis.ErrOwnedElsewhere) {
		err = errors.Join(command.ErrExecutionConnectionConflict, err)
	}
	entry.claimErr = err
	close(entry.claimed)
	if err != nil {
		m.forget(entry, workID)
		m.workers.Done()
		return err
	}
	go m.run(entry, client)
	return nil
}

func (m *NativeExecutions) claim(ctx context.Context, workID string, binding *command.ConnectExecution, entry *nativeExecution) (*execd.Client, error) {
	gen, target, err := m.ownership.Claim(ctx, workID, binding)
	if err != nil {
		return nil, err
	}
	raw, _, err := m.ownership.Snapshot(ctx, workID)
	var state *nativeprocess.ProtocolState
	if err == nil {
		state, err = nativeprocess.RestoreState(target, raw)
	}
	if err == nil {
		err = m.resumeReading(ctx, target, gen, state)
	}
	var buffered []dispatcherredis.InputEntry
	if err == nil {
		buffered, err = m.buffer.Entries(ctx, workID)
	}
	var client *execd.Client
	if err == nil {
		client, err = execd.NewClient(m.client, target.Endpoint, target.AccessToken)
	}
	if err != nil {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, m.ownership.Release(release, workID, gen))
	}
	entry.mu.Lock()
	entry.target, entry.gen, entry.ProtocolState = target, gen, state
	entry.mirror(buffered)
	entry.written, entry.unsettled = map[string]bool{}, map[string]settlement{}
	entry.feedback = newFeedbackQueue(m.feedback, m.logger)
	entry.mu.Unlock()
	return client, nil
}

// forget removes an entry that no longer reads its stream.
func (m *NativeExecutions) forget(entry *nativeExecution, workID string) {
	m.mu.Lock()
	if m.connections[workID] == entry {
		delete(m.connections, workID)
	}
	m.mu.Unlock()
}

// snapshot writes the stream's state at a boundary; the caller holds entry.mu.
// A write this replica's generation no longer permits ends reading.
func (m *NativeExecutions) snapshot(ctx context.Context, entry *nativeExecution) error {
	raw, err := nativeprocess.EncodeSnapshot(entry.target, entry.ProtocolState)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = m.ownership.SaveSnapshot(bounded, entry.target.WorkID, entry.gen, raw)
	if errors.Is(err, dispatcherredis.ErrFenced) {
		return entry.disconnect(errFenced)
	}
	if err != nil {
		// State is rebuildable: a missed snapshot only lengthens a later replay.
		m.logger.WarnContext(ctx, "native stream snapshot failed", "agent_work_id", entry.target.WorkID, "error", err)
		return nil
	}
	entry.dirty = false
	return nil
}

func (e *nativeExecution) disconnect(err error) error {
	e.broken, e.attached = true, false
	if e.cancel != nil {
		e.cancel(err)
	}
	if e.connection != nil {
		_ = e.connection.Close()
	}
	return err
}

// ObserveProcess answers for the running process from memory; only the
// replica reading the stream can, so routing sends the query there.
func (m *NativeExecutions) ObserveProcess(ctx context.Context, workID, processID string) (application.ProcessObservation, error) {
	if err := ctx.Err(); err != nil {
		return application.ProcessObservation{}, err
	}
	entry, err := m.find(workID)
	if err != nil {
		return application.ProcessObservation{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	// An exited process awaits only Agent's acknowledgement of its exit fact.
	if entry.ProtocolState == nil || entry.target.ProcessID != processID || entry.Exited {
		return application.ProcessObservation{State: application.ProcessPending}, nil
	}
	observation := entry.Observation()
	observation.Attached = entry.attached && !entry.broken
	return observation, nil
}

func (m *NativeExecutions) find(workID string) (*nativeExecution, error) {
	if strings.TrimSpace(workID) == "" {
		return nil, command.ErrExecutionConnection
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, command.ErrExecutionNotReady
	}
	entry, ok := m.connections[workID]
	if !ok {
		return nil, command.ErrNoActiveConnection
	}
	return entry, nil
}
