//go:build integration

package agent_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/go-jimu/components/taskqueue"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	accountintegrationv1 "github.com/talgent-work/contracts/gen/go/account/integration/v1"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
)

// queuedTasks records the tasks a policy queues, with the options they were
// queued under.
type queuedTasks struct {
	mu      sync.Mutex
	tasks   []taskqueue.Task
	retries []int
}

func (q *queuedTasks) Enqueue(_ context.Context, task taskqueue.Task, opts ...taskqueue.EnqueueOption) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, task)
	retries, _ := taskqueue.NewEnqueueOptions(opts...).MaxRetry()
	q.retries = append(q.retries, retries)
	return nil
}

// Account's Application Removed queues one bounded close per unclosed Work of
// that Application, and nothing for another Application's or a person's
// Work; each close stops a running Work first, as any close does.
func TestARemovedApplicationsWorksAreClosed(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk, billing := m.as("application", supportDesk), m.as("application", billingBot)
	person := m.as("user", uuid.NewString())

	first := uuid.NewString()
	work, err := desk.start(t, first, "Answer ticket 42")
	require.NoError(t, err)
	m.runs.ready(t, m.ctx, work)
	// Further input hands the initial task input to the Work's buffer; once
	// both settled and the Turn finished, the running Work may close.
	input := uuid.NewString()
	_, err = desk.work.SendMessage(desk.ctx, &agentpublicv1.SendMessageRequest{WorkId: work, InputId: input, Text: "Also mention the deadline"})
	require.NoError(t, err)
	for _, settled := range []string{first, input} {
		m.port.settle(settled)
		require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_INPUT_SETTLED, InputId: settled, InputOutcome: agentprivatev1.InputOutcome_INPUT_OUTCOME_COMPLETED}))
	}
	require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 20, TurnCompleted: true}))
	others := []string{}
	for _, owner := range []ownerClients{billing, person} {
		other, err := owner.start(t, uuid.NewString(), "Unrelated")
		require.NoError(t, err)
		others = append(others, other)
	}

	queued := &queuedTasks{}
	recovery := application.NewRecovery(m.app, queued, nil, nil, nil, nil, nil)
	payload := &accountintegrationv1.ApplicationRemovedV1{EventId: uuid.NewString(), ApplicationId: supportDesk}
	fact, err := integrationmessage.New(integrationmessage.KindOf(payload), payload, integrationmessage.WithKey(supportDesk))
	require.NoError(t, err)
	handler := messagehandler.NewApplicationRemovalHandler(recovery)
	require.NoError(t, handler.Handle(m.ctx, fact))

	require.Len(t, queued.tasks, 1, "only the removed Application's Work is closed")
	task := queued.tasks[0]
	require.Equal(t, agenttask.CloseRemovedApplicationWorkType, task.Type())
	require.Equal(t, "agent:removed-application-work:"+work, task.ID())
	require.Equal(t, agenttask.RemovedApplicationCloseRetries, queued.retries[0])

	var close taskqueue.Processor
	for _, processor := range taskprocessor.RecoveryProcessors(recovery, slog.Default()) {
		if processor.TaskType() == agenttask.CloseRemovedApplicationWorkType {
			close = processor
		}
	}
	require.NotNil(t, close)
	require.NoError(t, close.Process(m.ctx, task))
	closing, err := m.app.Works.Get(m.ctx, work)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, closing.State, "a running Work stops before it closes")
	require.NotEmpty(t, closing.CloseRequestReference, "the close applies once it has stopped")
	require.NoError(t, close.Process(m.ctx, task), "a repeated close confirms it")

	for i, owner := range []ownerClients{billing, person} {
		view, err := owner.work.GetWork(owner.ctx, &agentpublicv1.GetWorkRequest{WorkId: others[i]})
		require.NoError(t, err)
		require.NotEqual(t, "closed", view.GetWork().GetState())
		untouched, err := m.app.Works.Get(m.ctx, others[i])
		require.NoError(t, err)
		require.Empty(t, untouched.CloseRequestReference, "another owner's Work is not closed")
	}

	queued.tasks, queued.retries = nil, nil
	require.NoError(t, handler.Handle(m.ctx, fact), "a repeated fact queues the closes of the Works still unclosed")
	require.Len(t, queued.tasks, 1)
	require.Equal(t, task.ID(), queued.tasks[0].ID(), "keyed by the Work, so the queued task absorbs it")
}
