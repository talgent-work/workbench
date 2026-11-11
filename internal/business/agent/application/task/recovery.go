// Package task defines Agent's internal recovery task contracts.
//
// Every task is keyed by the one object it recovers, so a task already waiting,
// retrying or running for that object absorbs further wakes: the running
// attempt reads current state, and the periodic sweep covers a change it missed.
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
)

// NewReconcileExecution wakes the execution's short steps.
func NewReconcileExecution(executionID string) (taskqueue.Task, error) {
	return keyed(ReconcileExecutionType, RecoveryQueue, "execution", executionID, &agenttaskv1.ReconcileExecutionTaskV1{ExecutionId: executionID})
}

// NewRunLongExecutionStep wakes the execution's long step on the preparation
// queue, whose workers short steps never wait for.
func NewRunLongExecutionStep(executionID string) (taskqueue.Task, error) {
	return keyed(RunLongExecutionStepType, PreparationQueue, "execution-long-step", executionID, &agenttaskv1.RunLongExecutionStepTaskV1{ExecutionId: executionID})
}

func NewRecoverWork(workID string) (taskqueue.Task, error) {
	return keyed(RecoverWorkType, RecoveryQueue, "work", workID, &agenttaskv1.RecoverWorkTaskV1{WorkId: workID})
}

// NewRequestWorkReclamation queues a Closed Work's Work Reclamation Request,
// keyed by the Work. A request naming no Work is refused: Storage would have
// nothing to reclaim.
func NewRequestWorkReclamation(workID string) (taskqueue.Task, error) {
	if strings.TrimSpace(workID) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	return keyed(RequestWorkReclamationType, RecoveryQueue, "workspace-reclamation", workID, &agenttaskv1.RequestWorkspaceReclamationTaskV1{WorkId: workID})
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
