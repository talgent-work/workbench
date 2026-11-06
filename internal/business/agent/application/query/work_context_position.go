package query

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkContextPosition is where one of a Work's calls returned in its
// context: an opaque position a new Work of the same definition may start
// from. Returned is false while the call has not returned, or when the Work
// made no such call or is outside the Project.
type WorkContextPosition struct {
	Returned bool
	Position string
}

// CallPositionReader reads the Harness position of the message holding a
// call's result in a Work's own stream; found is false while there is none.
type CallPositionReader interface {
	FindCallPosition(ctx context.Context, workID, callID string) (position string, found bool, err error)
}

// ResolveWorkContextPositionHandler is the Work Context Position Query. The
// position is after the call's result, never inside an unfinished call.
type ResolveWorkContextPositionHandler struct {
	origins   WorkOriginQueryRepository
	positions CallPositionReader
}

func NewResolveWorkContextPositionHandler(origins WorkOriginQueryRepository, positions CallPositionReader) *ResolveWorkContextPositionHandler {
	return &ResolveWorkContextPositionHandler{origins: origins, positions: positions}
}

func (h *ResolveWorkContextPositionHandler) Handle(ctx context.Context, projectID, workID, callID string) (WorkContextPosition, error) {
	projectID, workID, callID = strings.TrimSpace(projectID), strings.TrimSpace(workID), strings.TrimSpace(callID)
	if projectID == "" || workID == "" || callID == "" {
		return WorkContextPosition{}, domain.ErrInvalidWork
	}
	origin, err := h.origins.GetWorkOrigin(ctx, workID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return WorkContextPosition{}, nil
	}
	if err != nil {
		return WorkContextPosition{}, err
	}
	if origin.ProjectID != projectID {
		return WorkContextPosition{}, nil
	}
	position, found, err := h.positions.FindCallPosition(ctx, workID, callID)
	if err != nil || !found || strings.TrimSpace(position) == "" {
		return WorkContextPosition{}, err
	}
	return WorkContextPosition{Returned: true, Position: position}, nil
}
