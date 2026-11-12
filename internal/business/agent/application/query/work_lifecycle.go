package query

import (
	"context"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkLifecycleEvent is one committed lifecycle transition of a Work. Stops,
// resumes and restarts appear as events on the Work's one continuous timeline.
type WorkLifecycleEvent struct {
	State      domain.WorkState
	OccurredAt time.Time
}

type ListWorkLifecycleEvents struct{ ActorID, WorkID string }

type ListWorkLifecycleEventsHandler struct {
	authorization access.WorkAuthorizer
	reader        WorkQueryRepository
}

func NewListWorkLifecycleEventsHandler(authorization access.WorkAuthorizer, reader WorkQueryRepository) *ListWorkLifecycleEventsHandler {
	return &ListWorkLifecycleEventsHandler{authorization: authorization, reader: reader}
}

func (h *ListWorkLifecycleEventsHandler) Handle(ctx context.Context, request ListWorkLifecycleEvents) ([]WorkLifecycleEvent, error) {
	if strings.TrimSpace(request.WorkID) == "" {
		return nil, domain.ErrInvalidWork
	}
	if err := h.authorization.AuthorizeWork(ctx, request.ActorID, request.WorkID); err != nil {
		return nil, err
	}
	return h.reader.ListWorkLifecycleEvents(ctx, request.WorkID)
}
