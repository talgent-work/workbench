package task_test

import (
	"testing"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	agenttaskv1 "github.com/talgent-work/workbench/gen/agent/task/v1"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"google.golang.org/protobuf/proto"
)

func TestRecoveryTasksAreKeyedByTheObjectTheyRecover(t *testing.T) {
	for name, tc := range map[string]struct {
		build   func(string) (taskqueue.Task, error)
		kind    taskqueue.TaskType
		queue   string
		key     string
		payload proto.Message
		id      func(proto.Message) string
	}{
		"execution":    {agenttask.NewReconcileExecution, agenttask.ReconcileExecutionType, agenttask.RecoveryQueue, "agent:execution:id-1", &agenttaskv1.ReconcileExecutionTaskV1{}, func(m proto.Message) string { return m.(*agenttaskv1.ReconcileExecutionTaskV1).GetExecutionId() }},
		"long step":    {agenttask.NewRunLongExecutionStep, agenttask.RunLongExecutionStepType, agenttask.PreparationQueue, "agent:execution-long-step:id-1", &agenttaskv1.RunLongExecutionStepTaskV1{}, func(m proto.Message) string { return m.(*agenttaskv1.RunLongExecutionStepTaskV1).GetExecutionId() }},
		"work":         {agenttask.NewRecoverWork, agenttask.RecoverWorkType, agenttask.RecoveryQueue, "agent:work:id-1", &agenttaskv1.RecoverWorkTaskV1{}, func(m proto.Message) string { return m.(*agenttaskv1.RecoverWorkTaskV1).GetWorkId() }},
		"continuation": {agenttask.NewRecoverContinuation, agenttask.RecoverContinuationType, agenttask.RecoveryQueue, "agent:continuation:id-1", &agenttaskv1.RecoverContinuationTaskV1{}, func(m proto.Message) string { return m.(*agenttaskv1.RecoverContinuationTaskV1).GetWorkId() }},
		"preparation":  {agenttask.NewPrepareInitialExecution, agenttask.PrepareInitialExecutionType, agenttask.PreparationQueue, "agent:initial-execution:id-1", &agenttaskv1.PrepareInitialExecutionTaskV1{}, func(m proto.Message) string { return m.(*agenttaskv1.PrepareInitialExecutionTaskV1).GetWorkId() }},
	} {
		t.Run(name, func(t *testing.T) {
			task, err := tc.build("id-1")
			require.NoError(t, err)
			require.Equal(t, tc.kind, task.Type())
			require.Equal(t, tc.queue, task.Queue())
			require.Equal(t, tc.key, task.ID())
			require.NoError(t, proto.Unmarshal(task.Payload(), tc.payload))
			require.Equal(t, "id-1", tc.id(tc.payload))
			_, err = tc.build(" ")
			require.Error(t, err, "a task without its object cannot be keyed")
		})
	}
}

// A failed sweep is archived; a fixed key would let it suppress every later one.
func TestSweepHasNoFixedKey(t *testing.T) {
	task, err := agenttask.NewSweepRecovery()
	require.NoError(t, err)
	require.Equal(t, agenttask.SweepRecoveryType, task.Type())
	require.Equal(t, agenttask.RecoveryQueue, task.Queue())
	require.Empty(t, task.ID())
}
