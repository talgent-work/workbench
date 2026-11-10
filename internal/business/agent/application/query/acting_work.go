package query

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// actingWait bounds how long an action waits for a delivered reply to resolve
// its Work's required response, polling at actingPoll; an Agent may call a
// tool the moment it receives the reply, before the response is recorded
// (Reply resolution wait).
const (
	actingWait = 2 * time.Second
	actingPoll = 20 * time.Millisecond
)

// AdmitAgentAction names the Work whose Agent wants to act.
type AdmitAgentAction struct {
	WorkID string
}

// AdmitAgentActionHandler answers the Acting Work Query Contract: whether a
// Work may act now, as AgentWork's Admit Agent action decides it.
type AdmitAgentActionHandler struct {
	works            domain.AgentWorkRepository
	requiredResponse RequiredResponseReader
}

func NewAdmitAgentActionHandler(works domain.AgentWorkRepository, requiredResponse RequiredResponseReader) *AdmitAgentActionHandler {
	return &AdmitAgentActionHandler{works: works, requiredResponse: requiredResponse}
}

// Handle returns once the Work acts, or refuses with ErrWorkNotActing when it
// is not Working or its required response stays unresolved through
// actingWait.
func (h *AdmitAgentActionHandler) Handle(ctx context.Context, request AdmitAgentAction) error {
	deadline := time.Now().Add(actingWait)
	for {
		work, err := h.works.Get(ctx, request.WorkID)
		if err != nil {
			return err
		}
		awaiting, err := h.requiredResponse(ctx, work.ID)
		if err != nil {
			return err
		}
		err = work.AdmitAgentAction(awaiting)
		// Wait only when the response's resolution alone would admit it.
		if err == nil || work.AdmitAgentAction(false) != nil || time.Now().Add(actingPoll).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(actingPoll):
		}
	}
}
