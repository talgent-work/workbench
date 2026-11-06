package scopemaintenance

import (
	"context"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
)

type recordingEnqueuer struct {
	keys      []string
	processAt []time.Time
}

func (e *recordingEnqueuer) Enqueue(_ context.Context, task componenttaskqueue.Task, opts ...componenttaskqueue.EnqueueOption) error {
	e.keys = append(e.keys, task.ID())
	e.processAt = append(e.processAt, componenttaskqueue.NewEnqueueOptions(opts...).ProcessAt())
	return nil
}

// Intent (real): requests within one window share an observation that runs
// after the window; a request during a running observation gets a later one.
func TestScheduleObservationGroupsRequestsByWindow(t *testing.T) {
	registry := componenttaskqueue.NewSchemaRegistry()
	require.NoError(t, RegisterTaskSchemas(registry, "scm-maintenance"))
	enqueuer := &recordingEnqueuer{}
	scheduler := NewScheduler(enqueuer, registry)
	start := time.Date(2026, 10, 13, 8, 0, 1, 0, time.UTC)
	for _, offset := range []time.Duration{0, 3 * time.Second, 6 * time.Second} {
		scheduler.now = func() time.Time { return start.Add(offset) }
		require.NoError(t, scheduler.ScheduleObservation(t.Context(), "auth-1"))
	}
	require.Equal(t, enqueuer.keys[0], enqueuer.keys[1])
	require.NotEqual(t, enqueuer.keys[1], enqueuer.keys[2])
	require.Equal(t, time.Date(2026, 10, 13, 8, 0, 5, 0, time.UTC), enqueuer.processAt[0])
	require.Equal(t, time.Date(2026, 10, 13, 8, 0, 10, 0, time.UTC), enqueuer.processAt[2])
	require.ErrorIs(t, scheduler.ScheduleObservation(t.Context(), ""), ErrInvalidPayload)
}
