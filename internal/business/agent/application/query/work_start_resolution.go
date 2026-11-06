package query

import (
	"context"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ResolvedWorkStart is one Work's start as a Causal Chain shows it. A Work
// that does not exist or belongs to another Project is reported as unknown.
type ResolvedWorkStart struct {
	WorkID    string
	Known     bool
	StartedBy string
	StartedAt time.Time
}

// MaxResolvedWorkStarts bounds one request; callers asking about more Works
// split them into requests of at most this many.
const MaxResolvedWorkStarts = 200

// WorkOriginsReader reads the origins of several Works at once; a Work that
// does not exist is absent from the result.
type WorkOriginsReader interface {
	ListWorkOrigins(ctx context.Context, workIDs []string) (map[string]WorkOrigin, error)
}

// ResolveWorkStartsHandler reports who started each Work of one Project and
// when, in one read, so the caller can show where each Work began without
// asking Work by Work.
type ResolveWorkStartsHandler struct {
	origins WorkOriginsReader
}

func NewResolveWorkStartsHandler(origins WorkOriginsReader) *ResolveWorkStartsHandler {
	return &ResolveWorkStartsHandler{origins: origins}
}

func (h *ResolveWorkStartsHandler) Handle(ctx context.Context, projectID string, workIDs []string) ([]ResolvedWorkStart, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || len(workIDs) > MaxResolvedWorkStarts {
		return nil, domain.ErrInvalidWork
	}
	ids := make([]string, 0, len(workIDs))
	for _, workID := range workIDs {
		if workID = strings.TrimSpace(workID); workID != "" {
			ids = append(ids, workID)
		}
	}
	origins, err := h.origins.ListWorkOrigins(ctx, ids)
	if err != nil {
		return nil, err
	}
	results := make([]ResolvedWorkStart, 0, len(workIDs))
	for _, workID := range workIDs {
		result := ResolvedWorkStart{WorkID: strings.TrimSpace(workID)}
		if origin, ok := origins[result.WorkID]; ok && origin.ProjectID == projectID {
			result.Known, result.StartedBy, result.StartedAt = true, origin.StartedBy, origin.StartedAt
		}
		results = append(results, result)
	}
	return results, nil
}
