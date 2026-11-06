package dispatcher

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

type scriptedReporter struct {
	mu       sync.Mutex
	err      error
	reported []command.ExecutionObservation
	release  chan struct{}
}

func (r *scriptedReporter) ReportExecution(ctx context.Context, observation command.ExecutionObservation) error {
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reported = append(r.reported, observation)
	return r.err
}

func (*scriptedReporter) ReportConnectionEnded(context.Context, string, string) error { return nil }

func (r *scriptedReporter) kinds() []command.ExecutionObservationKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kinds []command.ExecutionObservationKind
	for _, observation := range r.reported {
		kinds = append(kinds, observation.Kind)
	}
	return kinds
}

// Agent refusing an exit fact is permanent: the queue stops retrying it and
// the stream may end instead of waiting forever.
func TestRefusedExitFactIsNotRetried(t *testing.T) {
	reporter := &scriptedReporter{err: command.ErrExecutionFeedbackRejected}
	queue := newFeedbackQueue(reporter, slog.Default())
	defer queue.close()
	queue.push(command.ExecutionObservation{WorkID: "work", ProcessID: "process", Kind: command.ExecutionExited})
	select {
	case <-queue.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("a refused exit fact kept the stream waiting")
	}
	require.Len(t, reporter.kinds(), 1)
}

// A full queue drops superseded progress before any other observation and
// never the exit fact.
// A full queue drops a Progress or SubAgents snapshot that a later one of its
// kind already supersedes; the exit fact is never dropped.
func TestFullQueueDropsSupersededSnapshotsAndKeepsTheExit(t *testing.T) {
	reporter := &scriptedReporter{release: make(chan struct{})}
	queue := newFeedbackQueue(reporter, slog.Default())
	defer queue.close()
	queue.push(command.ExecutionObservation{Kind: command.ExecutionTurnStarted})
	queue.push(command.ExecutionObservation{Kind: command.ExecutionExited})
	queue.push(command.ExecutionObservation{Kind: command.ExecutionWorkProgressUpdated, ObservationSequence: 1})
	queue.push(command.ExecutionObservation{Kind: command.ExecutionSubAgentsUpdated, ObservationSequence: 2})
	for len(queueItems(queue)) < feedbackCapacity {
		queue.push(command.ExecutionObservation{Kind: command.ExecutionInputSettled})
	}
	queue.push(command.ExecutionObservation{Kind: command.ExecutionWorkProgressUpdated, ObservationSequence: 3})
	queue.push(command.ExecutionObservation{Kind: command.ExecutionSubAgentsUpdated, ObservationSequence: 4})
	items := queueItems(queue)
	require.Len(t, items, feedbackCapacity)
	require.Equal(t, command.ExecutionExited, items[1].Kind, "the exit fact is never dropped")
	var snapshots []int64
	for _, item := range items {
		if item.Kind == command.ExecutionWorkProgressUpdated || item.Kind == command.ExecutionSubAgentsUpdated {
			snapshots = append(snapshots, item.ObservationSequence)
		}
	}
	require.Equal(t, []int64{3, 4}, snapshots, "only the superseded snapshots went")
	close(reporter.release)
}

// The latest snapshot of a kind has no successor to replace it, so a full
// queue keeps it and drops another observation instead.
func TestFullQueueKeepsTheLatestSnapshot(t *testing.T) {
	reporter := &scriptedReporter{release: make(chan struct{})}
	queue := newFeedbackQueue(reporter, slog.Default())
	defer queue.close()
	queue.push(command.ExecutionObservation{Kind: command.ExecutionTurnStarted})
	queue.push(command.ExecutionObservation{Kind: command.ExecutionSubAgentsUpdated})
	queue.push(command.ExecutionObservation{Kind: command.ExecutionWorkProgressUpdated})
	for len(queueItems(queue)) < feedbackCapacity {
		queue.push(command.ExecutionObservation{Kind: command.ExecutionInputSettled})
	}
	queue.push(command.ExecutionObservation{Kind: command.ExecutionTurnFinished})
	items := queueItems(queue)
	require.Len(t, items, feedbackCapacity)
	require.Equal(t, command.ExecutionSubAgentsUpdated, items[1].Kind)
	require.Equal(t, command.ExecutionWorkProgressUpdated, items[2].Kind)
	require.Equal(t, command.ExecutionTurnFinished, items[len(items)-1].Kind)
	close(reporter.release)
}

func queueItems(q *feedbackQueue) []command.ExecutionObservation {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]command.ExecutionObservation(nil), q.items...)
}
