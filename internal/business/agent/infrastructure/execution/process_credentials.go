package execution

import (
	"context"
	"crypto/subtle"
	"errors"
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

var errWorkCredentialRejected = errors.New("agent: Work credential rejected")

// VerifyWorkCredential verifies the native process credential issued to the
// Work's current live environment. Retiring the execution revokes the
// credential.
func (q *ExecutionQueryRepository) VerifyWorkCredential(ctx context.Context, workID, credential string) error {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(credential) == "" {
		return errWorkCredentialRejected
	}
	record, found, err := q.records.live(ctx, workID)
	if err != nil {
		return err
	}
	if !found || record.SandboxID == "" {
		return errWorkCredentialRejected
	}
	issued, err := issuedWorkCredential(record)
	if err != nil {
		return err
	}
	if issued == "" || subtle.ConstantTimeCompare([]byte(issued), []byte(credential)) != 1 {
		return errWorkCredentialRejected
	}
	return nil
}

// issuedWorkCredential reads the Work credential that the execution's
// realization recorded in its specification.
func issuedWorkCredential(record allocationRecord) (string, error) {
	switch record.Realization {
	case realizationCloud:
		return cloudEnvironment{}.workCredential(record)
	case realizationManaged:
		return ManagedEnvironment{}.workCredential(record)
	default:
		return "", oops.With("realization", record.Realization).Errorf("agent: unknown execution realization")
	}
}
