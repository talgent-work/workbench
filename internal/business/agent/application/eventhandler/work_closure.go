package eventhandler

import (
	"context"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkClosures carries out the consequences of closing a Work by waking the
// Work's recovery, which owns them durably: a running Work is stopped first
// and closed once its execution ends, and entering Closing empties its input
// buffer. Without recovery wired, the periodic sweep carries them out.
type WorkClosures struct {
	// Wake enqueues the Work's recovery; it never fails the event.
	Wake func(ctx context.Context, workID string)
}

var _ domainevent.Handler = (*WorkClosures)(nil)

func NewWorkClosures() *WorkClosures { return &WorkClosures{} }

func (*WorkClosures) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindExecutionEnded, domain.EventKindClosing}
}

func (h *WorkClosures) Handle(ctx context.Context, event domainevent.Event) {
	var workID string
	switch e := event.(type) {
	case domain.WorkExecutionEnded:
		workID = e.WorkID
	case domain.WorkClosingEntered:
		workID = e.WorkID
	default:
		return
	}
	if h.Wake != nil {
		h.Wake(ctx, workID)
	}
}
