package taskprocessor_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
)

func closeAttempt(retried int) context.Context {
	return taskqueue.ContextWithExecutionInfo(context.Background(), taskqueue.NewExecutionInfo(
		taskqueue.WithExecutionTaskID("agent:removed-application-work:work-1"), taskqueue.WithExecutionRetryCount(retried),
		taskqueue.WithExecutionMaxRetry(agenttask.RemovedApplicationCloseRetries)))
}

// A removed Application's Work that keeps refusing its close is retried up to
// the ceiling; the attempt that exhausts it logs an error naming the Work.
func TestARemovedApplicationsWorkCloseEndsAtItsCeilingAsAnObservableFailure(t *testing.T) {
	// The Work's definition names no such Owner, so every close is refused.
	refusing := access.NewWorkAuthorizer(func(context.Context, string) ([]string, bool, error) { return []string{"someone-else"}, true, nil })
	app := &application.Application{Commands: application.Commands{CloseWork: command.NewCloseWorkHandler(nil, refusing, nil, nil)}}
	var logs bytes.Buffer
	recovery := application.NewRecovery(app, nil, nil, nil, nil, nil, nil)
	processor := processorFor(t, taskprocessor.RecoveryProcessors(recovery, slog.New(slog.NewJSONHandler(&logs, nil))), agenttask.CloseRemovedApplicationWorkType)
	task, err := agenttask.NewCloseRemovedApplicationWork("work-1", "app-1")
	require.NoError(t, err)
	require.Equal(t, "agent:removed-application-work:work-1", task.ID())

	require.ErrorIs(t, processor.Process(closeAttempt(agenttask.RemovedApplicationCloseRetries-1), task), access.ErrAccessDenied, "a failure before the ceiling is retried")
	require.NotContains(t, logs.String(), "removed application work close failed")

	require.Error(t, processor.Process(closeAttempt(agenttask.RemovedApplicationCloseRetries), task))
	require.Contains(t, logs.String(), `"level":"ERROR","msg":"removed application work close failed"`)
	require.Contains(t, logs.String(), `"work_id":"work-1"`)
	require.Contains(t, logs.String(), `"application_id":"app-1"`)
}
