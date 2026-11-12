package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// CloseWorkHandler is the one way to close a Work permanently, for the Portal
// and for business callers alike. A running Work is stopped first; its close
// request applies once it has stopped, through ApplyKeptClose.
type CloseWorkHandler struct {
	control sessionControl
	buffer  DispatcherPort
}

func NewCloseWorkHandler(sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer, executions domain.AgentExecutionRepository, buffer DispatcherPort) *CloseWorkHandler {
	return &CloseWorkHandler{control: sessionControl{sessions: sessions, access: authorization, executions: executions}, buffer: buffer}
}

func (h *CloseWorkHandler) Handle(ctx context.Context, request WorkControl) error {
	return h.control.apply(ctx, request, func(ctx context.Context, work *domain.AgentWork, requestID string) error {
		facts, err := h.facts(ctx, work)
		if err != nil {
			return err
		}
		return work.Close(requestID, facts)
	})
}

// CloseForRemovedApplication closes a Work of an Application that Account
// removed (Close removed Application's Works). Agent itself closes it, the
// one exception to Standalone Work control: the Application, which can no
// longer call, must still own the Work's definition, so a task naming another
// Work is refused. A running Work is stopped first between Turns, as any
// close; a Turn still active refuses it, and the caller repeats the close.
func (h *CloseWorkHandler) CloseForRemovedApplication(ctx context.Context, applicationID, workID string) error {
	return h.Handle(ctx, WorkControl{SubjectID: applicationID, WorkID: workID, RequestID: uuid.Must(uuid.NewV7()).String()})
}

// ApplyKeptClose applies the close request a Work kept while it stopped. The
// Work's recovery calls it, woken by WorkExecutionEnded or found by the sweep;
// it does nothing for a Work with no such request or still stopping. Recovery
// empties the input buffer of a Work that entered Closing.
func (h *CloseWorkHandler) ApplyKeptClose(ctx context.Context, workID string) error {
	work, err := h.control.sessions.Get(ctx, workID)
	if err != nil {
		return err
	}
	if !work.KeptCloseApplicable() {
		return nil
	}
	facts, err := h.facts(ctx, work)
	if err != nil {
		return err
	}
	if err := work.Close(work.CloseRequestReference, facts); err != nil {
		return err
	}
	return h.control.sessions.Save(ctx, work)
}

// facts supplies the obligations a close must not discard.
func (h *CloseWorkHandler) facts(ctx context.Context, work *domain.AgentWork) (domain.ClosureFacts, error) {
	var facts domain.ClosureFacts
	if work.Closed() {
		return facts, nil
	}
	queued, err := startQueued(ctx, h.control.executions, work)
	if err != nil {
		return facts, err
	}
	facts.StartQueued = queued
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	facts.InputPending = buffered.Pending()
	return facts, err
}
