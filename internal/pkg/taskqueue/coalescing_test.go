package taskqueue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
)

// queuedTasks records what a coalescing enqueues, or refuses while refusal is
// set.
type queuedTasks struct {
	mu      sync.Mutex
	tasks   []componenttaskqueue.Task
	refusal error
}

func (q *queuedTasks) Enqueue(_ context.Context, task componenttaskqueue.Task, _ ...componenttaskqueue.EnqueueOption) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.refusal != nil {
		return q.refusal
	}
	q.tasks = append(q.tasks, task)
	return nil
}

func (q *queuedTasks) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks)
}

func coalescingOver(t *testing.T) (*Coalescing, *queuedTasks, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	queued := &queuedTasks{}
	return NewCoalescing(Option{RedisAddr: server.Addr()}, queued, slog.New(slog.DiscardHandler)), queued, server
}

func reconcileTask(t *testing.T) componenttaskqueue.Task {
	t.Helper()
	task, err := componenttaskqueue.New(componenttaskqueue.Definition{Type: "taskqueue.test.reconcile", Queue: DefaultQueueName}, []byte(`{}`))
	require.NoError(t, err)
	return task
}

func attempt(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func nothing(context.Context) error { return nil }

// A wake collapses into a task that has not started, which reads the state
// after it; once that task's attempt has started, a wake queues a new task.
func TestCoalescedWakeCollapsesOnlyIntoATaskNotStarted(t *testing.T) {
	coalescing, queued, _ := coalescingOver(t)
	ctx := attempt(t)
	require.NoError(t, coalescing.Wake(ctx, "object-1", reconcileTask(t)))
	require.NoError(t, coalescing.Wake(ctx, "object-1", reconcileTask(t)))
	require.Equal(t, 1, queued.count(), "the waiting task covers the second wake")
	require.NoError(t, coalescing.Wake(ctx, "object-2", reconcileTask(t)))
	require.Equal(t, 2, queued.count(), "another object has its own task")

	require.NoError(t, coalescing.Run(ctx, "object-1", func(context.Context) error {
		require.NoError(t, coalescing.Wake(ctx, "object-1", reconcileTask(t)))
		return nil
	}, nothing))
	require.Equal(t, 3, queued.count(), "a wake from elsewhere during the attempt queues a task after it")
}

// A change the attempt holding the object commits itself wakes the object
// under that attempt, which decides again on its own; it queues no task that
// would run the object once more for nothing. Another object's wake is queued.
func TestCoalescedWakeUnderTheHoldingAttemptQueuesNothing(t *testing.T) {
	coalescing, queued, _ := coalescingOver(t)
	ctx := attempt(t)
	reruns := 0
	require.NoError(t, coalescing.Run(ctx, "object-1", func(ctx context.Context) error {
		require.NoError(t, coalescing.Wake(ctx, "object-1", reconcileTask(t)))
		require.NoError(t, coalescing.Wake(ctx, "object-2", reconcileTask(t)))
		return nil
	}, func(context.Context) error { reruns++; return nil }))
	require.Equal(t, 1, queued.count(), "only the other object's task")
	require.Zero(t, reruns)
}

// A task meeting an attempt that holds its object does not run beside it: it
// hands its wake over, and the holder wakes the object again once it releases
// it. An attempt nobody met wakes nothing.
func TestCoalescedWakeMeetingARunningAttemptRunsAgainAfterIt(t *testing.T) {
	coalescing, _, _ := coalescingOver(t)
	ctx := attempt(t)
	reruns := 0
	rerun := func(context.Context) error { reruns++; return nil }
	contenderRan := false

	require.NoError(t, coalescing.Run(ctx, "object-1", func(ctx context.Context) error {
		require.NoError(t, coalescing.Run(ctx, "object-1", func(context.Context) error {
			contenderRan = true
			return nil
		}, rerun))
		require.Zero(t, reruns, "the wake waits for the holder to end")
		return nil
	}, rerun))
	require.False(t, contenderRan, "attempts for one object never run together")
	require.Equal(t, 1, reruns, "the holder wakes the object again")

	require.NoError(t, coalescing.Run(ctx, "object-1", nothing, rerun))
	require.Equal(t, 1, reruns, "the handed wake was taken with the release")
}

// An attempt that outlived its hold cannot tell whether a wake was handed to
// it, and wakes its object again; a hold left by a dead process lapses, after
// which attempts run again.
func TestCoalescedLapsedHoldWakesAgain(t *testing.T) {
	coalescing, _, server := coalescingOver(t)
	ctx := attempt(t)
	reruns := 0
	rerun := func(context.Context) error { reruns++; return nil }
	require.NoError(t, coalescing.Run(ctx, "object-1", func(context.Context) error {
		server.FastForward(2 * time.Minute)
		return nil
	}, rerun))
	require.Equal(t, 1, reruns)

	key := coalescingKeys("object-1").hold
	require.NoError(t, server.Set(key, "dead-process"))
	server.SetTTL(key, 10*time.Second)
	ran := false
	require.NoError(t, coalescing.Run(ctx, "object-1", func(context.Context) error { ran = true; return nil }, rerun))
	require.False(t, ran, "a held object runs no second attempt")
	server.FastForward(11 * time.Second)
	require.NoError(t, coalescing.Run(ctx, "object-1", func(context.Context) error { ran = true; return nil }, rerun))
	require.True(t, ran, "the dead process's hold lapsed")
}

// A wake whose task could not be queued leaves no mark, so the next wake
// queues the task.
func TestCoalescedRefusedWakeLeavesNoMark(t *testing.T) {
	coalescing, queued, _ := coalescingOver(t)
	ctx := attempt(t)
	unavailable := errors.New("queue unavailable")
	queued.refusal = unavailable
	require.ErrorIs(t, coalescing.Wake(ctx, "object-1", reconcileTask(t)), unavailable)
	queued.refusal = nil
	require.NoError(t, coalescing.Wake(ctx, "object-1", reconcileTask(t)))
	require.Equal(t, 1, queued.count())
}

// The run's and the rerun's failures reach the provider, which retries the
// task.
func TestCoalescedFailuresAreReturned(t *testing.T) {
	coalescing, _, _ := coalescingOver(t)
	ctx := attempt(t)
	failed := errors.New("step failed")
	require.ErrorIs(t, coalescing.Run(ctx, "object-1", func(context.Context) error { return failed }, nothing), failed)

	refused := errors.New("wake refused")
	err := coalescing.Run(ctx, "object-1", func(ctx context.Context) error {
		return coalescing.Run(ctx, "object-1", nothing, nothing)
	}, func(context.Context) error { return refused })
	require.ErrorIs(t, err, refused)
}

// A task ID would collapse a wake into an attempt already running, and an
// attempt without a deadline would hold its object for good.
func TestCoalescingRefusesKeyedTasksAndUnboundedAttempts(t *testing.T) {
	coalescing, queued, _ := coalescingOver(t)
	keyed, err := componenttaskqueue.New(componenttaskqueue.Definition{Type: "taskqueue.test.reconcile", Queue: DefaultQueueName}, []byte(`{}`), componenttaskqueue.WithID("object-1"))
	require.NoError(t, err)
	require.ErrorIs(t, coalescing.Wake(attempt(t), "object-1", keyed), ErrKeyedCoalescedTask)
	require.Zero(t, queued.count())
	require.ErrorIs(t, coalescing.Run(t.Context(), "object-1", nothing, nothing), ErrUnboundedCoalescedRun)
}
