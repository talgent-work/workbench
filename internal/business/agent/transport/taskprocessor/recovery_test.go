package taskprocessor_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
)

type reconciled struct {
	command.ExecutionEnvironmentPort
	ids []string
}

func (r *reconciled) ReconcileAllocation(_ context.Context, id string) (command.AllocationStep, error) {
	r.ids = append(r.ids, id)
	return command.AllocationSettled, nil
}
func (r *reconciled) PrepareAllocation(_ context.Context, id string) error {
	r.ids = append(r.ids, "prepare:"+id)
	return nil
}
func (r *reconciled) ReconnectAllocation(_ context.Context, id string) error {
	r.ids = append(r.ids, "reconnect:"+id)
	return nil
}
func (r *reconciled) ObserveSandbox(_ context.Context, id string) (command.AllocationStep, error) {
	r.ids = append(r.ids, "observe:"+id)
	return command.AllocationSettled, nil
}
func (r *reconciled) DeliverCredentials(_ context.Context, id string) error {
	r.ids = append(r.ids, "deliver:"+id)
	return nil
}
func (r *reconciled) SyncInputs(_ context.Context, id string) error {
	r.ids = append(r.ids, "inputs:"+id)
	return nil
}
func (*reconciled) MarkCredentialsChanged(context.Context, string) ([]string, error) { return nil, nil }
func (*reconciled) MarkInputsChanged(context.Context, string) ([]string, error)      { return nil, nil }
func (*reconciled) LiveAllocation(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (*reconciled) ListOutstandingAllocations(context.Context, string, int) ([]command.AllocationStatus, error) {
	return nil, nil
}

func processorFor(t *testing.T, processors []taskqueue.Processor, kind taskqueue.TaskType) taskqueue.Processor {
	t.Helper()
	for _, processor := range processors {
		if processor.TaskType() == kind {
			return processor
		}
	}
	t.Fatalf("no processor for %s", kind)
	return nil
}

func TestAllocationTaskDelegatesItsAllocationOnce(t *testing.T) {
	environments := &reconciled{}
	processors := taskprocessor.RecoveryProcessors(application.NewRecovery(&application.Application{}, nil, environments, nil, nil), slog.Default())
	task, err := agenttask.NewReconcileAllocation("allocation-1")
	require.NoError(t, err)
	require.NoError(t, processorFor(t, processors, agenttask.ReconcileAllocationType).Process(context.Background(), task))
	require.Equal(t, []string{"allocation-1"}, environments.ids)
}

func TestOnboardingTasksDelegateTheirStep(t *testing.T) {
	environments := &reconciled{}
	recovery := application.NewRecovery(&application.Application{}, nil, environments, nil, nil)
	for _, step := range []struct {
		processors []taskqueue.Processor
		kind       taskqueue.TaskType
		task       func(string) (taskqueue.Task, error)
	}{
		{taskprocessor.PreparationProcessors(recovery, slog.Default()), agenttask.PrepareAllocationType, agenttask.NewPrepareAllocation},
		{taskprocessor.RecoveryProcessors(recovery, slog.Default()), agenttask.ReconnectAllocationType, agenttask.NewReconnectAllocation},
		{taskprocessor.RecoveryProcessors(recovery, slog.Default()), agenttask.ObserveSandboxType, agenttask.NewObserveSandbox},
		{taskprocessor.RecoveryProcessors(recovery, slog.Default()), agenttask.DeliverCredentialsType, agenttask.NewDeliverCredentials},
		{taskprocessor.RecoveryProcessors(recovery, slog.Default()), agenttask.SyncInputsType, agenttask.NewSyncInputs},
	} {
		task, err := step.task("allocation-1")
		require.NoError(t, err)
		require.NoError(t, processorFor(t, step.processors, step.kind).Process(context.Background(), task))
	}
	require.Equal(t, []string{"prepare:allocation-1", "reconnect:allocation-1", "observe:allocation-1", "deliver:allocation-1", "inputs:allocation-1"}, environments.ids)
}

// A malformed task is dropped rather than archived, so it cannot hold its key
// against later wakes for the same object.
func TestMalformedRecoveryTaskIsDroppedWithoutDelegating(t *testing.T) {
	environments := &reconciled{}
	processors := append(taskprocessor.RecoveryProcessors(application.NewRecovery(&application.Application{}, nil, environments, nil, nil), slog.Default()), taskprocessor.PreparationProcessors(nil, slog.Default())...)
	for _, processor := range processors {
		malformed, err := taskqueue.New(taskqueue.Definition{Type: processor.TaskType()}, []byte{0xff, 0xff}, taskqueue.WithID("agent:allocation:id"))
		require.NoError(t, err)
		require.NoError(t, processor.Process(context.Background(), malformed), processor.TaskType())
	}
	require.Empty(t, environments.ids)
}
