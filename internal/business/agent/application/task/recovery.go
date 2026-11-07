// Package task defines Agent's internal recovery task contracts.
//
// Every task is keyed by the one object it recovers, so a task already waiting,
// retrying or running for that object absorbs further wakes: the running
// attempt reads current state, and the periodic sweep covers a change it missed.
package task

import (
	"strings"

	"github.com/go-jimu/components/taskqueue"
	agenttaskv1 "github.com/talgent-work/workbench/gen/agent/task/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// RecoveryQueue carries short recovery steps for allocations, Works and requests.
	RecoveryQueue = "agent-recovery"
	// PreparationQueue carries work that may hold a worker for minutes: an
	// allocation's environment preparation and initial execution preparation.
	PreparationQueue = "agent-preparation"
)

const (
	ReconcileAllocationType     taskqueue.TaskType = "agent.allocation.reconcile.v1"
	PrepareAllocationType       taskqueue.TaskType = "agent.allocation.prepare.v1"
	ReconnectAllocationType     taskqueue.TaskType = "agent.allocation.reconnect.v1"
	ObserveSandboxType          taskqueue.TaskType = "agent.allocation.observe-sandbox.v1"
	DeliverCredentialsType      taskqueue.TaskType = "agent.allocation.deliver-credentials.v1"
	SyncInputsType              taskqueue.TaskType = "agent.allocation.sync-inputs.v1"
	RecoverWorkType             taskqueue.TaskType = "agent.work.recover.v1"
	RecoverContinuationType     taskqueue.TaskType = "agent.continuation.recover.v1"
	PrepareInitialExecutionType taskqueue.TaskType = "agent.initial-execution.prepare.v1"
	SweepRecoveryType           taskqueue.TaskType = "agent.recovery.sweep.v1"
	// RequestWorkspaceReclamationType delivers a Closed Work's one request to
	// Storage; it is a delivery, not a recovery obligation of the Work.
	RequestWorkspaceReclamationType taskqueue.TaskType = "agent.workspace.request-reclamation.v1"
)

func NewReconcileAllocation(allocationID string) (taskqueue.Task, error) {
	return keyed(ReconcileAllocationType, RecoveryQueue, "allocation", allocationID, &agenttaskv1.ReconcileAllocationTaskV1{AllocationId: allocationID})
}

func NewPrepareAllocation(allocationID string) (taskqueue.Task, error) {
	return keyed(PrepareAllocationType, PreparationQueue, "allocation-preparation", allocationID, &agenttaskv1.PrepareAllocationTaskV1{AllocationId: allocationID})
}

func NewReconnectAllocation(allocationID string) (taskqueue.Task, error) {
	return keyed(ReconnectAllocationType, RecoveryQueue, "allocation-reconnect", allocationID, &agenttaskv1.ReconnectAllocationTaskV1{AllocationId: allocationID})
}

func NewDeliverCredentials(allocationID string) (taskqueue.Task, error) {
	return keyed(DeliverCredentialsType, RecoveryQueue, "allocation-credentials", allocationID, &agenttaskv1.DeliverCredentialsTaskV1{AllocationId: allocationID})
}

func NewSyncInputs(allocationID string) (taskqueue.Task, error) {
	return keyed(SyncInputsType, RecoveryQueue, "allocation-inputs", allocationID, &agenttaskv1.SyncInputsTaskV1{AllocationId: allocationID})
}

func NewObserveSandbox(allocationID string) (taskqueue.Task, error) {
	return keyed(ObserveSandboxType, RecoveryQueue, "allocation-sandbox", allocationID, &agenttaskv1.ObserveSandboxTaskV1{AllocationId: allocationID})
}

func NewRecoverWork(workID string) (taskqueue.Task, error) {
	return keyed(RecoverWorkType, RecoveryQueue, "work", workID, &agenttaskv1.RecoverWorkTaskV1{WorkId: workID})
}

func NewRecoverContinuation(workID string) (taskqueue.Task, error) {
	return keyed(RecoverContinuationType, RecoveryQueue, "continuation", workID, &agenttaskv1.RecoverContinuationTaskV1{WorkId: workID})
}

func NewPrepareInitialExecution(workID string) (taskqueue.Task, error) {
	return keyed(PrepareInitialExecutionType, PreparationQueue, "initial-execution", workID, &agenttaskv1.PrepareInitialExecutionTaskV1{WorkId: workID})
}

func NewRequestWorkspaceReclamation(workID, workspaceReference string) (taskqueue.Task, error) {
	if strings.TrimSpace(workspaceReference) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	return keyed(RequestWorkspaceReclamationType, RecoveryQueue, "workspace-reclamation", workID, &agenttaskv1.RequestWorkspaceReclamationTaskV1{WorkId: workID, WorkspaceReference: workspaceReference})
}

// NewSweepRecovery is the static envelope fired by the periodic sweep. It has
// no fixed key: a failed sweep is archived, and an archived task would hold a
// fixed key and suppress every later sweep. Overlap is bounded by uniqueness
// for one interval instead.
func NewSweepRecovery() (taskqueue.Task, error) {
	payload, err := proto.Marshal(&agenttaskv1.SweepRecoveryTaskV1{})
	if err != nil {
		return taskqueue.Task{}, err
	}
	return taskqueue.New(taskqueue.Definition{Type: SweepRecoveryType, Queue: RecoveryQueue}, payload)
}

// keyed builds a task whose provider identity is its kind and object, so
// duplicate wakes for one object collapse into one task.
func keyed(kind taskqueue.TaskType, queue, object, id string, payload proto.Message) (taskqueue.Task, error) {
	if strings.TrimSpace(id) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	encoded, err := proto.Marshal(payload)
	if err != nil {
		return taskqueue.Task{}, err
	}
	return taskqueue.New(taskqueue.Definition{Type: kind, Queue: queue}, encoded, taskqueue.WithID("agent:"+object+":"+id))
}
