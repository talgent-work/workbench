package taskprocessor_test

import (
	"context"
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

func (o *observed) Observe(_ context.Context, execution *domain.AgentExecution) (domain.EnvironmentObservation, error) {
	o.ids = append(o.ids, execution.ID)
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
	reconciliation := command.NewReconcileExecutionHandler(time.Now, domain.LeaseTerms{}, repository, environments, nil, unseated{}, nil)
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

// The reconciliation task delegates its execution once.
func TestReconcileTaskDelegatesItsExecutionOnce(t *testing.T) {
	environments := &observed{}
	recovery, id := recovery(t, environments)
	task, err := agenttask.NewReconcileExecution(id)
	require.NoError(t, err)
	require.NoError(t, processorFor(t, taskprocessor.RecoveryProcessors(recovery, slog.Default()), agenttask.ReconcileExecutionType).Process(context.Background(), task))
	require.Equal(t, []string{id}, environments.ids)
}

// The long step task is served only by the preparation workers, and decides
// its execution again before taking a step.
func TestLongStepTaskRunsOnlyOnThePreparationWorkers(t *testing.T) {
	environments := &observed{}
	recovery, id := recovery(t, environments)
	for _, processor := range taskprocessor.RecoveryProcessors(recovery, slog.Default()) {
		require.NotEqual(t, agenttask.RunLongExecutionStepType, processor.TaskType(), "no recovery worker waits for a long step")
	}
	task, err := agenttask.NewRunLongExecutionStep(id)
	require.NoError(t, err)
	require.NoError(t, processorFor(t, taskprocessor.PreparationProcessors(recovery, slog.Default()), agenttask.RunLongExecutionStepType).Process(context.Background(), task))
	require.Equal(t, []string{id}, environments.ids)
}

// removedTypes are the task types Agent no longer enqueues, by the workers
// that served them.
var removedTypes = []struct {
	preparation bool
	kind        taskqueue.TaskType
}{
	{false, "agent.allocation.reconcile.v1"},
	{false, "agent.allocation.reconnect.v1"},
	{false, "agent.allocation.observe-sandbox.v1"},
	{false, "agent.allocation.deliver-credentials.v1"},
	{false, "agent.allocation.sync-inputs.v1"},
	{false, "agent.work-creation.recover.v1"},
	{false, "agent.continuation.recover.v1"},
	{true, "agent.allocation.prepare.v1"},
	{true, "agent.initial-execution.prepare.v1"},
}

// Agent recovers through exactly five tasks: Reconcile execution, Run long
// execution step, Recover Work, Request workspace reclamation and the
// Recovery sweep.
func TestAgentRecoversThroughFiveTasks(t *testing.T) {
	recovery, _ := recovery(t, &observed{})
	removed := map[taskqueue.TaskType]bool{}
	for _, step := range removedTypes {
		removed[step.kind] = true
	}
	var served []taskqueue.TaskType
	for _, processor := range append(taskprocessor.RecoveryProcessors(recovery, slog.Default()), taskprocessor.PreparationProcessors(recovery, slog.Default())...) {
		if !removed[processor.TaskType()] {
			served = append(served, processor.TaskType())
		}
	}
	require.ElementsMatch(t, []taskqueue.TaskType{agenttask.ReconcileExecutionType, agenttask.RunLongExecutionStepType, agenttask.RecoverWorkType, agenttask.RequestWorkspaceReclamationType, agenttask.SweepRecoveryType}, served)
}

// A task queued under a type no longer enqueued is dropped without
// delegating, rather than retried without end; the sweep recovers its object.
func TestTasksOfRemovedTypesAreDropped(t *testing.T) {
	environments := &observed{}
	recovery, id := recovery(t, environments)
	for _, removed := range removedTypes {
		step := struct {
			processors []taskqueue.Processor
			kind       taskqueue.TaskType
		}{taskprocessor.RecoveryProcessors(recovery, slog.Default()), removed.kind}
		if removed.preparation {
			step.processors = taskprocessor.PreparationProcessors(recovery, slog.Default())
		}
		// The removed payloads carried the execution's identity as field 1.
		queued, err := taskqueue.New(taskqueue.Definition{Type: step.kind}, []byte{0x0a, 0x01, 0x78}, taskqueue.WithID("agent:allocation:"+id))
		require.NoError(t, err)
		require.NoError(t, processorFor(t, step.processors, step.kind).Process(context.Background(), queued), step.kind)
	}
	require.Empty(t, environments.ids)
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
