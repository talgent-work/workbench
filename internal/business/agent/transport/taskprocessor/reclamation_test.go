package taskprocessor_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
)

type refusingStorage struct {
	command.StoragePort
	requests int
}

func (s *refusingStorage) RequestWorkReclamation(context.Context, string) error {
	s.requests++
	return errors.New("storage unavailable")
}

// attempt is the context of a task attempt after retried failed ones.
func attempt(retried int) context.Context {
	return taskqueue.ContextWithExecutionInfo(context.Background(), taskqueue.NewExecutionInfo(
		taskqueue.WithExecutionTaskID("agent:workspace-reclamation:work-1"), taskqueue.WithExecutionRetryCount(retried), taskqueue.WithExecutionMaxRetry(agenttask.ReclamationRetries)))
}

// A Work Reclamation Request Storage keeps refusing is retried up to its
// ceiling; the attempt that exhausts it ends the reclamation as failed, with
// an error naming the Work and the cause.
func TestAReclamationRequestEndsAtItsCeilingAsAnObservableFailure(t *testing.T) {
	storage := &refusingStorage{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	recovery := application.NewRecovery(&application.Application{}, nil, nil, nil, nil, storage, nil)
	processor := processorFor(t, taskprocessor.RecoveryProcessors(recovery, logger), agenttask.RequestWorkReclamationType)
	task, err := agenttask.NewRequestWorkReclamation("work-1")
	require.NoError(t, err)

	require.Error(t, processor.Process(attempt(agenttask.ReclamationRetries-1), task), "a failure before the ceiling is retried")
	require.NotContains(t, logs.String(), "work reclamation failed")

	require.Error(t, processor.Process(attempt(agenttask.ReclamationRetries), task))
	require.Contains(t, logs.String(), `"level":"ERROR","msg":"work reclamation failed"`)
	require.Contains(t, logs.String(), `"work_id":"work-1"`)
	require.Contains(t, logs.String(), `"error":"storage unavailable"`)
	require.Equal(t, 2, storage.requests)
}
