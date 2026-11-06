package eventhandler

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type reclamationQueue struct {
	failures int
	queued   []string
}

func (q *reclamationQueue) RequestWorkspaceReclamation(_ context.Context, workID, reference string) error {
	if q.failures > 0 {
		q.failures--
		return errors.New("queue unavailable")
	}
	q.queued = append(q.queued, workID+"/"+reference)
	return nil
}

// Reaching Closed hands the Work's one reclamation request over once, riding
// out a brief queue outage; Agent keeps nothing to follow up.
func TestClosedWorkRequestsReclamationOnce(t *testing.T) {
	queue := &reclamationQueue{failures: 1}
	handler := NewWorkspaceReclamations(queue, slog.Default())
	handler.backoff = 0
	handler.Handle(context.Background(), domain.WorkClosedEvent{WorkID: "work-1", WorkspaceReference: "workspace-1"})
	require.Equal(t, []string{"work-1/workspace-1"}, queue.queued)
	handler.Handle(context.Background(), domain.WorkClosedEvent{WorkID: "work-2"})
	require.Len(t, queue.queued, 1, "a Work without a workspace has nothing to reclaim")
}
