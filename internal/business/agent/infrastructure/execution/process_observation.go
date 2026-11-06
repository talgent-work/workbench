package execution

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/samber/oops"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ProcessObservations is the environment authority's current view of a Work's
// Harness process: the Work's live allocation and Dispatcher's observation of
// the process launched in it. Without a launched process in a live
// allocation, the current start's process is not live yet.
type ProcessObservations struct {
	provisioning *Provisioning
	dispatcher   *DispatcherControl
}

var _ command.ProcessObservationPort = (*ProcessObservations)(nil)

func NewProcessObservations(provisioning *Provisioning, dispatcher *DispatcherControl) (*ProcessObservations, error) {
	if provisioning == nil || dispatcher == nil {
		return nil, errors.New("agent: provisioning and Dispatcher control are required")
	}
	return &ProcessObservations{provisioning: provisioning, dispatcher: dispatcher}, nil
}

func (o *ProcessObservations) ObserveProcess(ctx context.Context, workID string) (command.WorkProcessObservation, error) {
	// A deletion receipt can be replayed after another process has started.
	// Inspect the latest allocation for this Work, including retired allocations
	// whose actual sandbox removal is still pending; never trust the hint's owner.
	var record allocationRecord
	found, err := o.provisioning.db.Context(ctx).Where("session_id = ?", workID).Desc("created_at", "id").Get(&record)
	if err != nil {
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{}}, oops.Wrap(err)
	}
	if !found {
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending}}, nil
	}
	return o.observeAllocation(ctx, record)
}

func (o *ProcessObservations) IsCurrentProcess(ctx context.Context, workID, processID string) (bool, error) {
	record, found, err := o.provisioning.live(ctx, workID)
	if err != nil {
		return false, err
	}
	return found && processID != "" && record.ProcessID == processID, nil
}

// AcknowledgeExit records on the Work's live allocation that its process
// exited, when the exit fact names that process. The record authorizes
// releasing the environment; Dispatcher redelivers the fact until this succeeds.
func (o *ProcessObservations) AcknowledgeExit(ctx context.Context, workID, processID string) (bool, error) {
	if strings.TrimSpace(processID) == "" {
		return false, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		record, found, err := o.provisioning.live(ctx, workID)
		if err != nil {
			return false, err
		}
		if !found || record.ProcessID != processID {
			return false, nil
		}
		if record.ProcessExited {
			return true, nil
		}
		result, err := o.provisioning.db.Context(ctx).Exec("UPDATE execution_allocation SET process_exited = 1, updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND deleted_at = 0",
			o.provisioning.now().UnixMilli(), record.ID, record.Version)
		if err != nil {
			return false, oops.With("operation", "acknowledge_process_exit").Wrap(err)
		}
		if changed, err := result.RowsAffected(); err != nil {
			return false, oops.Wrap(err)
		} else if changed == 1 {
			return true, nil
		}
	}
	return false, errEnvironmentConflict
}

// environmentLost reports whether the allocation's environment is
// authoritatively gone.
func (o *ProcessObservations) environmentLost(ctx context.Context, record allocationRecord) (bool, error) {
	authority, err := o.provisioning.authority(record)
	if err != nil {
		return false, err
	}
	return authority.lost(ctx, record)
}

// Observe the fixed resource binding selected by the caller. Resource deletion
// must not look up another live allocation after this observation completes.
func (o *ProcessObservations) observeAllocation(ctx context.Context, record allocationRecord) (command.WorkProcessObservation, error) {
	if record.StartRejected {
		if record.SandboxID != "" {
			return command.WorkProcessObservation{}, errEnvironmentConflict
		}
		if record.DeletedAt != 0 {
			return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}}, nil
		}
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessStartRejected}}, nil
	}
	if record.SandboxID == "" {
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending}}, nil
	}
	workID := record.SessionID
	if record.DeletedAt == 0 {
		// The loss of a live allocation's environment is reported by the
		// environment authority's observation, so observing a
		// running process asks only Dispatcher.
		if record.ProcessID == "" {
			if abandoned(record, o.provisioning.now()) {
				return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessStartAbandoned}}, nil
			}
			return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending}}, nil
		}
		return o.dispatcher.ObserveProcess(ctx, workID, record.ProcessID)
	}
	released, err := o.retiredReleased(ctx, record)
	if err != nil {
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{}}, err
	}
	if released {
		// A retired allocation belongs to a completed resource obligation.
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}}, nil
	}
	if record.ProcessID == "" || record.ProcessExited {
		// An acknowledged exit was already applied to the Work, and its process
		// has no connection left for Dispatcher to observe; only the release
		// remains outstanding.
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending}}, nil
	}
	return o.dispatcher.ObserveProcess(ctx, workID, record.ProcessID)
}

// retiredReleased reports whether a retired allocation's compute is gone: as
// its authority observes it, or else as its completed removal obligation
// records it.
func (o *ProcessObservations) retiredReleased(ctx context.Context, record allocationRecord) (bool, error) {
	authority, err := o.provisioning.authority(record)
	if err != nil {
		return false, err
	}
	if observed, ok := authority.(observedCompute); ok {
		return observed.released(ctx, record)
	}
	released, err := o.provisioning.db.Context(ctx).Where("id = ? AND completed = 1 AND deleted_at = 0", record.ID).Exist(&removalRecord{})
	if err != nil {
		return false, oops.Wrap(err)
	}
	return released, nil
}

func (c *DispatcherControl) ObserveProcess(ctx context.Context, workID, processID string) (command.WorkProcessObservation, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(processID) == "" || strings.TrimSpace(c.token) == "" {
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{}}, domain.ErrExecutionRequest
	}
	request := connect.NewRequest(&dispatcherprivatev1.ObserveProcessRequest{WorkId: workID, ProcessId: processID})
	request.Header().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.ObserveProcess(ctx, request)
	if err != nil {
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{}}, controlError(ctx, workID, "observe process", err)
	}
	switch response.Msg.GetState() {
	case dispatcherprivatev1.ProcessState_PROCESS_STATE_PENDING:
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending}}, nil
	case dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING:
		turn := response.Msg.GetTurn()
		return command.WorkProcessObservation{
			ProcessObservation: domain.ProcessObservation{State: domain.ProcessRunning, Ready: response.Msg.Ready},
			Turn:               domain.TurnObservation{Sequence: turn.GetSequence(), Active: turn.GetActive(), Completed: turn.GetCompleted()},
			PhaseSequence:      response.Msg.PhaseSequence, RequiredInteractionIDs: response.Msg.RequiredInteractionIds,
			Attachment: sandboxAttachment(response.Msg),
		}, nil
	default:
		return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{}}, oops.With("agent_work_id", workID).Errorf("unknown native process state %v", response.Msg.GetState())
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

// WorkCredentials verifies the native process credential issued to the Work's
// current live environment. Retiring the allocation revokes the credential.
type WorkCredentials struct{ provisioning *Provisioning }

var _ query.WorkCredentialVerifier = (*WorkCredentials)(nil)

var errWorkCredentialRejected = errors.New("agent: Work credential rejected")

func NewWorkCredentials(provisioning *Provisioning) (*WorkCredentials, error) {
	if provisioning == nil {
		return nil, errors.New("agent: provisioning is required")
	}
	return &WorkCredentials{provisioning: provisioning}, nil
}

func (w *WorkCredentials) VerifyWorkCredential(ctx context.Context, workID, credential string) error {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(credential) == "" {
		return errWorkCredentialRejected
	}
	record, found, err := w.provisioning.live(ctx, workID)
	if err != nil {
		return err
	}
	if !found || record.SandboxID == "" {
		return errWorkCredentialRejected
	}
	authority, err := w.provisioning.authority(record)
	if err != nil {
		return err
	}
	issued, err := authority.workCredential(record)
	if err != nil {
		return err
	}
	if issued == "" || subtle.ConstantTimeCompare([]byte(issued), []byte(credential)) != 1 {
		return errWorkCredentialRejected
	}
	return nil
}
