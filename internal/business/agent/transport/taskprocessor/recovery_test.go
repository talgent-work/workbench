package taskprocessor_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
)

// observed records each observation the reconciliation makes of a queued
// execution, which decides no step, so every task ends at its decision.
type observed struct {
	command.ExecutionEnvironmentPort
	ids []string
}

func (o *observed) Observe(_ context.Context, execution *domain.AgentExecution, full bool) (domain.EnvironmentObservation, error) {
	o.ids = append(o.ids, fmt.Sprintf("%s:%t", execution.ID, full))
	return domain.EnvironmentObservation{}, nil
}

type executions struct {
	domain.AgentExecutionRepository
	execution *domain.AgentExecution
}

func (e executions) Get(_ context.Context, id string) (*domain.AgentExecution, error) {
	if id != e.execution.ID {
		return nil, domain.ErrExecutionNotFound
	}
	copied := *e.execution
	return &copied, nil
}

type unseated struct{ command.ComputerPort }

func (unseated) DeskSeated(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

// recovery reconciles one queued execution through environments.
func recovery(t *testing.T, environments *observed) (*application.Recovery, string) {
	t.Helper()
	queued, err := domain.BeginExecution(domain.ExecutionBeginning{
		Admitted:    domain.WorkStartAdmitted{WorkID: "work-1", StartAdmittedAt: time.Now(), DeskID: "desk-1", Waiting: true},
		Launch:      domain.LaunchSpecification{WorkID: "work-1", HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "home"}},
		Realization: domain.RealizationCloud,
	})
	require.NoError(t, err)
	repository := executions{execution: queued}
	reconciliation := command.NewReconcileExecutionHandler(time.Now, repository, environments, nil, unseated{}, nil, nil)
	return application.NewRecovery(&application.Application{}, nil, repository, nil, reconciliation, nil, nil), queued.ID
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

func TestReconcileTaskDelegatesItsExecutionOnce(t *testing.T) {
	environments := &observed{}
	recovery, id := recovery(t, environments)
	processors := taskprocessor.RecoveryProcessors(recovery, slog.Default())
	task, err := agenttask.NewReconcileAllocation(id)
	require.NoError(t, err)
	require.NoError(t, processorFor(t, processors, agenttask.ReconcileAllocationType).Process(context.Background(), task))
	require.Equal(t, []string{id + ":false"}, environments.ids)
}

// Each step task decides its execution again before taking its step; a
// sandbox observation reconciles it against every environment it owns.
func TestStepTasksDelegateTheirExecution(t *testing.T) {
	environments := &observed{}
	recovery, id := recovery(t, environments)
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
		task, err := step.task(id)
		require.NoError(t, err)
		require.NoError(t, processorFor(t, step.processors, step.kind).Process(context.Background(), task))
	}
	require.Equal(t, []string{id + ":false", id + ":false", id + ":true", id + ":false", id + ":false"}, environments.ids)
}

// A malformed task is dropped rather than archived, so it cannot hold its key
// against later wakes for the same object.
func TestMalformedRecoveryTaskIsDroppedWithoutDelegating(t *testing.T) {
	environments := &observed{}
	recovery, _ := recovery(t, environments)
	processors := append(taskprocessor.RecoveryProcessors(recovery, slog.Default()), taskprocessor.PreparationProcessors(nil, slog.Default())...)
	for _, processor := range processors {
		malformed, err := taskqueue.New(taskqueue.Definition{Type: processor.TaskType()}, []byte{0xff, 0xff}, taskqueue.WithID("agent:allocation:id"))
		require.NoError(t, err)
		require.NoError(t, processor.Process(context.Background(), malformed), processor.TaskType())
	}
	require.Empty(t, environments.ids)
}
