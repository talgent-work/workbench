package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// NewRequiredResponseReader reads whether a required interaction still needs
// a response. Agent matches Dispatcher's pending interactions with the
// replies its input buffer holds, as it does when applying execution feedback,
// so the Work's Waiting decision and this answer share one derivation.
func NewRequiredResponseReader(harness command.DispatcherPort) query.RequiredResponseReader {
	return func(ctx context.Context, workID string) (bool, error) {
		// The buffer is read first: a reply written and removed in between
		// leaves its interaction resolved, not unanswered.
		buffered, err := harness.ReadBuffer(ctx, workID)
		if err != nil {
			return false, err
		}
		pending, err := harness.ListPendingInteractions(ctx, workID)
		if err != nil {
			return false, err
		}
		ids := make([]string, len(pending))
		for i, item := range pending {
			ids[i] = item.ID
		}
		return buffered.Unanswered(ids), nil
	}
}
