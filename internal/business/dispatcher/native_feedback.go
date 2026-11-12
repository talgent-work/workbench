package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

const (
	// feedbackCapacity bounds a Work's undelivered observations. Beyond it an
	// observation is dropped: a Progress, SubAgents, permission mode or title snapshot a later one
	// supersedes first, otherwise the oldest other than the exit fact and the
	// latest snapshots, which nothing would replace. Observations are hints; Agent's recovery
	// observation of the process restores readiness, turns, interactions and
	// completed inputs a dropped one carried.
	feedbackCapacity = 1024
	feedbackAttempts = 3
	// exitAttempts bounds the delivery of the exit fact on one connection:
	// with delays doubling to exitRetryCeiling, about five minutes. A fact
	// still undelivered then ends the stream unacknowledged; the snapshot
	// keeps it, and the connection Agent's recovery makes next delivers it
	// again.
	exitAttempts      = 32
	feedbackTimeout   = 10 * time.Second
	exitRetryCeiling  = 10 * time.Second
	feedbackRetryBase = 500 * time.Millisecond
)

// feedbackQueue delivers one Work's observations to Agent in order, apart from
// reading the stream, so a slow Agent never stops the reader. An
// observation is a hint and is retried a few times; the exit fact is retried
// until Agent acknowledges it, up to exitAttempts.
type feedbackQueue struct {
	reporter command.ExecutionFeedbackReporter
	logger   *slog.Logger
	mu       sync.Mutex
	items    []command.ExecutionObservation
	idle     bool
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	// ctx bounds deliveries; closing the queue cancels one in flight.
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	// exited is closed once Agent acknowledges the exit fact; undelivered
	// once its delivery is given up at its ceiling.
	exited      chan struct{}
	exitOnce    sync.Once
	undelivered chan struct{}
	// retryBase and exitAttempts are feedbackRetryBase and exitAttempts,
	// shortened only by tests.
	retryBase    time.Duration
	exitAttempts int
}

func newFeedbackQueue(reporter command.ExecutionFeedbackReporter, logger *slog.Logger) *feedbackQueue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &feedbackQueue{reporter: reporter, logger: logger, idle: true, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), exited: make(chan struct{}), undelivered: make(chan struct{}), ctx: ctx, cancel: cancel, retryBase: feedbackRetryBase, exitAttempts: exitAttempts}
	go q.run()
	return q
}

// snapshot reports an observation each later one of its kind replaces whole.
func snapshot(kind command.ExecutionObservationKind) bool {
	return kind == command.ExecutionWorkProgressUpdated || kind == command.ExecutionSubAgentsUpdated || kind == command.ExecutionPermissionModeObserved || kind == command.ExecutionTitleObserved
}

// latest reports whether no later queued observation has the kind of item i.
func (q *feedbackQueue) latest(i int) bool {
	return !slices.ContainsFunc(q.items[i+1:], func(later command.ExecutionObservation) bool { return later.Kind == q.items[i].Kind })
}

func (q *feedbackQueue) push(observation command.ExecutionObservation) {
	q.mu.Lock()
	if len(q.items) >= feedbackCapacity {
		// The head may be in delivery and the exit fact is never dropped. A
		// snapshot a later one of its kind supersedes goes first; the latest
		// snapshot has nothing to replace it, so it stays.
		victim := 0
		for i := 1; i < len(q.items); i++ {
			kind := q.items[i].Kind
			if snapshot(kind) && (kind == observation.Kind || !q.latest(i)) {
				victim = i
				break
			}
			if victim == 0 && kind != command.ExecutionExited && !snapshot(kind) {
				victim = i
			}
		}
		dropped := q.items[victim]
		q.items = append(q.items[:victim], q.items[victim+1:]...)
		q.logger.Warn("native feedback dropped", "agent_work_id", dropped.WorkID, "kind", dropped.Kind)
	}
	q.items = append(q.items, observation)
	q.idle = false
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// drain waits until everything queued is delivered or given up, or the wait
// ends; a handover drains before the peer resumes reading.
func (q *feedbackQueue) drain(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		q.mu.Lock()
		idle := q.idle
		q.mu.Unlock()
		if idle {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		case <-q.done:
			return
		}
	}
}

func (q *feedbackQueue) close() {
	q.stopOnce.Do(func() { close(q.stop); q.cancel() })
	<-q.done
}

func (q *feedbackQueue) run() {
	defer close(q.done)
	for {
		q.mu.Lock()
		if len(q.items) == 0 {
			q.idle = true
			q.mu.Unlock()
			select {
			case <-q.wake:
				continue
			case <-q.stop:
				return
			}
		}
		observation := q.items[0]
		q.mu.Unlock()
		select {
		case <-q.stop:
			return
		default:
		}
		if !q.deliver(observation) {
			return
		}
		q.mu.Lock()
		q.items = q.items[1:]
		q.mu.Unlock()
	}
}

// deliver reports false when the queue stops before an exit fact is
// acknowledged, or gives the exit fact up at its ceiling; the resuming
// replica or the next connection redelivers it. Agent refusing the exit fact
// is permanent: it is logged and the stream ends as if acknowledged.
func (q *feedbackQueue) deliver(observation command.ExecutionObservation) bool {
	exit := observation.Kind == command.ExecutionExited
	delay := q.retryBase
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(q.ctx, feedbackTimeout)
		err := q.reporter.ReportExecution(ctx, observation)
		cancel()
		if err == nil {
			if exit {
				q.exitOnce.Do(func() { close(q.exited) })
			}
			return true
		}
		rejected := errors.Is(err, command.ErrExecutionFeedbackRejected) || errors.Is(err, command.ErrInvalidExecutionFeedback)
		if exit && rejected {
			q.logger.Error("native exit fact refused by Agent", "agent_work_id", observation.WorkID, "process_id", observation.ProcessID, "error", err)
			q.exitOnce.Do(func() { close(q.exited) })
			return true
		}
		if !exit && (rejected || attempt >= feedbackAttempts) {
			q.logger.Warn("native feedback not delivered", "agent_work_id", observation.WorkID, "kind", observation.Kind, "error", err)
			return true
		}
		if exit && attempt >= q.exitAttempts {
			q.logger.Error("native exit fact not delivered", "agent_work_id", observation.WorkID, "process_id", observation.ProcessID, "attempts", attempt, "error", err)
			close(q.undelivered)
			return false
		}
		q.logger.Warn("native feedback delivery failed; retrying", "agent_work_id", observation.WorkID, "kind", observation.Kind, "attempt", attempt, "error", err)
		select {
		case <-time.After(delay):
		case <-q.stop:
			return !exit
		}
		delay = min(delay*2, exitRetryCeiling)
	}
}
