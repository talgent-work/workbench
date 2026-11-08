// Package task defines Agent's internal recovery task contracts.
//
// Every task is keyed by the one object it recovers, so a task already waiting,
// retrying or running for that object absorbs further wakes: the running
// attempt reads current state, and the periodic sweep covers a change it missed.
package task

import (
	"math"
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

// Persistent is the policy of a task that is never abandoned: a recovery
// obligation, or a request whose delivery Agent took on. The worker's retry
// delay bounds how often a failing task is retried; timeout bounds one
// attempt.
func Persistent(timeout time.Duration) []taskqueue.EnqueueOption {
	return []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(math.MaxInt32), taskqueue.WithTimeout(timeout)}
}

const (
	ReconcileExecutionType   taskqueue.TaskType = "agent.execution.reconcile.v1"
	RunLongExecutionStepType taskqueue.TaskType = "agent.execution.run-long-step.v1"
	RecoverWorkType          taskqueue.TaskType = "agent.work.recover.v1"
	SweepRecoveryType        taskqueue.TaskType = "agent.recovery.sweep.v1"
	// RequestWorkReclamationType delivers a Closed Work's one request to
	// Storage; it is a delivery, not a recovery obligation of the Work.
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

func NewRequestWorkReclamation(workID string) (taskqueue.Task, error) {
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
