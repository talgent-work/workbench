package execution

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/samber/oops"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ObserveProcess reads Dispatcher's current observation of the named
// process.
func (c *DispatcherControl) ObserveProcess(ctx context.Context, workID, processID string) (command.WorkProcessObservation, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(processID) == "" || strings.TrimSpace(c.token) == "" {
		return command.WorkProcessObservation{}, domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.ObserveProcessRequest{WorkId: workID, ProcessId: processID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.ObserveProcess(ctx, request)
	if err != nil {
		return command.WorkProcessObservation{}, controlError(ctx, workID, "observe process", err)
	}
	switch response.GetState() {
	case dispatcherprivatev1.ProcessState_PROCESS_STATE_PENDING:
		return command.WorkProcessObservation{}, nil
	case dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING:
		turn := response.GetTurn()
		return command.WorkProcessObservation{
			Running: true, Ready: response.Ready,
			Turn:          domain.TurnObservation{Sequence: turn.GetSequence(), Active: turn.GetActive(), Completed: turn.GetCompleted()},
			PhaseSequence: response.PhaseSequence, RequiredInteractionIDs: response.RequiredInteractionIds,
			Attachment: sandboxAttachment(response),
		}, nil
	default:
		return command.WorkProcessObservation{}, oops.With("agent_work_id", workID).Errorf("unknown native process state %v", response.GetState())
	}
}

// sandboxAttachment reads Dispatcher's attachment from its observation; one
// without it, from a Dispatcher that does not report it, is unreported.
func sandboxAttachment(response *dispatcherprivatev1.ObserveProcessResponse) command.SandboxAttachment {
	switch {
	case response.Attached == nil:
		return command.AttachmentUnreported
	case response.GetAttached():
		return command.AttachmentHeld
	default:
		return command.AttachmentDetached
	}
}
