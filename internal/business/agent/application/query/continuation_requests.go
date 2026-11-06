package query

import (
	"context"
)

type WorkContinuation struct {
	RequestID string
	Pending   bool
}

type ContinuationReader interface {
	GetWorkContinuation(context.Context, string) (WorkContinuation, error)
	ListPendingContinuations(context.Context, string, int) ([]string, error)
}

type ListPendingContinuationsHandler struct{ reader ContinuationReader }

func NewListPendingContinuationsHandler(reader ContinuationReader) *ListPendingContinuationsHandler {
	return &ListPendingContinuationsHandler{reader: reader}
}
func (h *ListPendingContinuationsHandler) Handle(ctx context.Context, after string) ([]string, error) {
	return h.reader.ListPendingContinuations(ctx, after, 100)
}
