package dispatcher

import (
	"context"
	"sort"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// ListPendingInteractions reads the Harness requests from memory and whether
// each has a reply from the Work's input buffer, which may hold a reply
// another replica buffered a moment ago.
func (m *NativeExecutions) ListPendingInteractions(ctx context.Context, workID string) ([]application.PendingInteraction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := m.find(workID)
	if err != nil {
		return nil, err
	}
	buffered, err := m.buffer.List(ctx, workID)
	if err != nil {
		return nil, err
	}
	replied := map[string]bool{}
	for _, input := range buffered {
		if input.Reply != nil {
			replied[input.ID] = true
		}
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.ProtocolState == nil || entry.broken || !entry.attached {
		return nil, command.ErrExecutionNotReady
	}
	if entry.Exited {
		return nil, nil
	}
	ids := make([]string, 0, len(entry.Pending))
	for id := range entry.Pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]application.PendingInteraction, 0, len(ids))
	for _, id := range ids {
		request := entry.Pending[id].Request
		owner := entry.Projection.Requests[id]
		item := application.PendingInteraction{
			ID: id, ToolCallID: request.ToolUseID, ToolName: request.ToolName, Prompt: request.Title,
			CanAlwaysAllow: len(request.PermissionSuggestions) > 0, ReplySubmitted: replied[id], TurnID: owner.TurnID,
		}
		if owner.Agent != nil {
			agent := *owner.Agent
			item.Agent = &agent
		}
		if request.ToolName == "AskUserQuestion" {
			item.QuestionInputJSON = string(request.Input)
		}
		items = append(items, item)
	}
	return items, nil
}
