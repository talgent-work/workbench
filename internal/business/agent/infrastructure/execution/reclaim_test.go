package execution

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

type reclamationFeedback struct {
	requests []command.ExecutionFeedback
	err      error
	// observe stands for Agent observing the Work's process when it applies
	// the feedback, as ApplyExecutionFeedback does; nil observes nothing.
	observe func(context.Context, command.ExecutionFeedback) error
}

func (h *reclamationFeedback) Handle(ctx context.Context, request command.ExecutionFeedback) error {
	h.requests = append(h.requests, request)
	if h.err != nil || h.observe == nil {
		return h.err
	}
	return h.observe(ctx, request)
}
