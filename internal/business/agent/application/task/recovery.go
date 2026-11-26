// Package task defines Agent's internal recovery task contracts.
//
// Every task concerns the one object it recovers. An obligation's tasks
// (Reconcile execution, its long step, Recover Work) are coalesced by that
// object: a wake collapses only into a task for it that has not started, and
// a wake meeting a running attempt runs again after it, so a change committed
// while an attempt runs is never left to the periodic sweep. Deliveries are
// keyed by their object, so a task already waiting, retrying or running for it
// absorbs a repeated one.
package task

import (
	"strings"
	"time"

	"github.com/go-jimu/components/taskqueue"
	agenttaskv1 "github.com/talgent-work/workbench/gen/agent/task/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// RecoveryQueue carries short recovery steps for executions and Works.
	RecoveryQueue = "agent-recovery"
	// PreparationQueue carries work that may hold a worker for minutes: an
	// execution's long step.
	PreparationQueue = "agent-preparation"
)

// RecoveryAttemptTimeout bounds one attempt of a task on the recovery queue.
const RecoveryAttemptTimeout = 2 * time.Minute

// RecoveryRetries bounds the retries of one wake of a recovery task. A wake
// that exhausts them is archived with its last error, which the worker logs
// as an error; the obligation itself stays on its object, so the periodic
// sweep wakes it again once per interval, and the next wake replaces the
// archived task. An execution's long step ends no later than its Startup
// Deadline, which fails the start naming its last setback.
const RecoveryRetries = 10

// Recovery is the policy of a recovery task: at most RecoveryRetries retries,
// each attempt bounded by timeout.
func Recovery(timeout time.Duration) []taskqueue.EnqueueOption {
	return []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(RecoveryRetries), taskqueue.WithTimeout(timeout)}
}

// ReclamationRetries bounds the delivery of a Work Reclamation Request: with
// the recovery queue's delays, Storage may be away for about twenty minutes.
// Agent keeps no reclamation obligation, so a request that exhausts them is
// a terminal reclamation failure, logged as an error naming the Work.
const ReclamationRetries = 25

// Reclamation is the policy of a Work Reclamation Request task.
func Reclamation() []taskqueue.EnqueueOption {
	return []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(ReclamationRetries), taskqueue.WithTimeout(RecoveryAttemptTimeout)}
}

// RemovedApplicationCloseRetries bounds the close of one Work of a removed
// Application. Agent keeps no obligation for it, so a close that exhausts
// them is logged as an error naming the Work, which then stops at its idle
// timeout.
const RemovedApplicationCloseRetries = 10

// RemovedApplicationClose is the policy of a removed Application's Work close.
func RemovedApplicationClose() []taskqueue.EnqueueOption {
	return []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(RemovedApplicationCloseRetries), taskqueue.WithTimeout(RecoveryAttemptTimeout)}
}

// InputChangeNoticeRetries bounds the delivery of one input change notice.
// Agent keeps no obligation for it, so a delivery that exhausts them is
// logged as an error naming the Work, and nothing wakes it again.
const InputChangeNoticeRetries = 10

// InputChangeNotice is the policy of an input change notice delivery.
func InputChangeNotice() []taskqueue.EnqueueOption {
	return []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(InputChangeNoticeRetries), taskqueue.WithTimeout(RecoveryAttemptTimeout)}
}

const (
	ReconcileExecutionType   taskqueue.TaskType = "agent.execution.reconcile.v1"
	RunLongExecutionStepType taskqueue.TaskType = "agent.execution.run-long-step.v1"
	RecoverWorkType          taskqueue.TaskType = "agent.work.recover.v1"
	SweepRecoveryType        taskqueue.TaskType = "agent.recovery.sweep.v1"
	// RequestWorkReclamationType delivers a Closed Work's one request to
	// Storage; it is a delivery, not a recovery obligation of the Work. Its
	// wire name, its payload message (RequestWorkspaceReclamationTaskV1) and
	// its task key keep their earlier "workspace" spelling: tasks already
	// queued under them must still be decoded and deduplicated.
	RequestWorkReclamationType taskqueue.TaskType = "agent.workspace.request-reclamation.v1"
	// CloseRemovedApplicationWorkType closes one Work of a removed
	// Application.
	CloseRemovedApplicationWorkType taskqueue.TaskType = "agent.work.close-removed-application.v1"
	// DeliverInputChangeNoticeType delivers one Work the notice of one
	// change in its located inputs.
	DeliverInputChangeNoticeType taskqueue.TaskType = "agent.work.deliver-input-change-notice.v1"
)

// NewReconcileExecution wakes the execution's short steps; ExecutionObject
// coalesces it.
func NewReconcileExecution(executionID string) (taskqueue.Task, error) {
	return coalesced(ReconcileExecutionType, RecoveryQueue, executionID, &agenttaskv1.ReconcileExecutionTaskV1{ExecutionId: executionID})
}

// NewRunLongExecutionStep wakes the execution's long step on the preparation
// queue, whose workers short steps never wait for; LongStepObject coalesces
// it.
func NewRunLongExecutionStep(executionID string) (taskqueue.Task, error) {
	return coalesced(RunLongExecutionStepType, PreparationQueue, executionID, &agenttaskv1.RunLongExecutionStepTaskV1{ExecutionId: executionID})
}

// NewRecoverWork wakes the Work's own obligations; WorkObject coalesces it.
func NewRecoverWork(workID string) (taskqueue.Task, error) {
	return coalesced(RecoverWorkType, RecoveryQueue, workID, &agenttaskv1.RecoverWorkTaskV1{WorkId: workID})
}

// ExecutionObject, LongStepObject and WorkObject name the objects whose
// tasks are coalesced: an execution's short steps, its long step, which runs
// beside them, and a Work's own obligations.
func ExecutionObject(executionID string) string { return "agent:execution:" + executionID }
func LongStepObject(executionID string) string  { return "agent:execution-long-step:" + executionID }
func WorkObject(workID string) string           { return "agent:work:" + workID }

// NewRequestWorkReclamation queues a Closed Work's Work Reclamation Request,
// keyed by the Work. A request naming no Work is refused: Storage would have
// nothing to reclaim.
func NewRequestWorkReclamation(workID string) (taskqueue.Task, error) {
	if strings.TrimSpace(workID) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	return keyed(RequestWorkReclamationType, RecoveryQueue, "workspace-reclamation", workID, &agenttaskv1.RequestWorkspaceReclamationTaskV1{WorkId: workID})
}

// NewCloseRemovedApplicationWork queues the close of one Work of a removed
// Application, keyed by the Work, so a repeated fact collapses into the task
// already queued.
func NewCloseRemovedApplicationWork(workID, applicationID string) (taskqueue.Task, error) {
	if strings.TrimSpace(applicationID) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	return keyed(CloseRemovedApplicationWorkType, RecoveryQueue, "removed-application-work", workID,
		&agenttaskv1.CloseRemovedApplicationWorkTaskV1{WorkId: workID, ApplicationId: applicationID})
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

// coalesced builds a task without a provider identity; its object coalesces
// its wakes.
func coalesced(kind taskqueue.TaskType, queue, id string, payload proto.Message) (taskqueue.Task, error) {
	if strings.TrimSpace(id) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	encoded, err := proto.Marshal(payload)
	if err != nil {
		return taskqueue.Task{}, err
	}
	return taskqueue.New(taskqueue.Definition{Type: kind, Queue: queue}, encoded)
}

// keyed builds a task whose provider identity is its kind and object, so
// duplicate deliveries for one object collapse into one task.
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
