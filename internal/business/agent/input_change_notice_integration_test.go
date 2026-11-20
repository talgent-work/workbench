//go:build integration

package agent_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/go-jimu/components/taskqueue"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
)

// noticeTime is when every file change of these cases was committed.
var noticeTime = time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)

// inputChanges drives Notify changed inputs as the module runs it: Storage's
// File Change Published Fact enters Agent's subscriber, which queues the
// notices, and the recovery queue's processor delivers each one.
type inputChanges struct {
	m       *lifecycleModule
	queued  *queuedTasks
	handler *messagehandler.FileChangedHandler
	deliver taskqueue.Processor
}

func newInputChanges(m *lifecycleModule, logger *slog.Logger) *inputChanges {
	queued := &queuedTasks{}
	// No Managed execution reads these inputs, so the input copy sync this
	// fact also wakes finds nothing to mark.
	recovery := application.NewRecovery(m.app, queued, nil, m.app.ExecutionQueries, nil, nil, nil)
	changes := &inputChanges{m: m, queued: queued, handler: messagehandler.NewFileChangedHandler(recovery)}
	for _, processor := range taskprocessor.RecoveryProcessors(recovery, logger) {
		if processor.TaskType() == agenttask.DeliverInputChangeNoticeType {
			changes.deliver = processor
		}
	}
	require.NotNil(m.t, changes.deliver)
	return changes
}

// publish hands the subscriber one file change in the inputs Directory of
// the owner, then runs every notice task it queued.
func (c *inputChanges) publish(eventID string, owner *storageintegrationv1.OwnerFact, change *storageintegrationv1.FileChangedV1) {
	c.m.t.Helper()
	change.EventId, change.OccurredAt = eventID, timestamppb.New(noticeTime)
	change.Directory = &storageintegrationv1.Directory{Owner: []*storageintegrationv1.OwnerFact{owner}, Name: "inputs"}
	fact, err := integrationmessage.New(integrationmessage.KindOf(change), change)
	require.NoError(c.m.t, err)
	require.NoError(c.m.t, c.handler.Handle(c.m.ctx, fact))
	tasks := c.queued.take()
	for _, task := range tasks {
		require.NoError(c.m.t, c.deliver.Process(c.m.ctx, task))
	}
}

// take removes and returns the queued tasks.
func (q *queuedTasks) take() []taskqueue.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	tasks := q.tasks
	q.tasks, q.retries = nil, nil
	return tasks
}

func workInputs(workID string) *storageintegrationv1.OwnerFact {
	return &storageintegrationv1.OwnerFact{Condition: "work", Value: workID}
}

func added(name string) *storageintegrationv1.FileChangedV1 {
	return &storageintegrationv1.FileChangedV1{Name: name, Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED, ActorUserId: workOwner}
}

// A person's upload, replacement, rename and deletion in a Standalone Work's
// inputs each reach its Agent as one input change notice naming only the
// file, the operation and the time; a redelivered fact adds nothing.
func TestAStandaloneWorkHearsOfEachChangeInItsInputs(t *testing.T) {
	m := newLifecycleModule(t).start()
	person := m.as("user", uuid.NewString())
	work, err := person.start(t, uuid.NewString(), "Summarize the report")
	require.NoError(t, err)
	changes := newInputChanges(m, slog.Default())

	upload := uuid.NewString()
	changes.publish(upload, workInputs(work), added("report.pdf"))
	changes.publish(upload, workInputs(work), added("report.pdf"))
	changes.publish(uuid.NewString(), workInputs(work), &storageintegrationv1.FileChangedV1{Name: "report.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_REPLACED})
	changes.publish(uuid.NewString(), workInputs(work), &storageintegrationv1.FileChangedV1{Name: "final.pdf", PreviousName: "report.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED})
	changes.publish(uuid.NewString(), workInputs(work), &storageintegrationv1.FileChangedV1{Name: "final.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_DELETED})

	require.ElementsMatch(t, []string{
		"Summarize the report",
		`Input file "report.pdf" was added at 2026-11-20T08:30:00Z.`,
		`Input file "report.pdf" was replaced at 2026-11-20T08:30:00Z.`,
		`Input file "report.pdf" was renamed to "final.pdf" at 2026-11-20T08:30:00Z.`,
		`Input file "final.pdf" was deleted at 2026-11-20T08:30:00Z.`,
	}, m.port.bufferedFor(work), "the start message first, then one notice per change")
}

// A stopped Work's notice waits in its input buffer without starting it; a
// Work with a File Home hears of its inputs from that File Home's owner, and
// a Closed Work hears nothing.
func TestOnlyAnUnclosedWorkWithoutAFileHomeIsNotified(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	stopped := seedWork(t, m.db, definition, workSeed{subject: "stopped", state: domain.WorkStopped}).ID
	closed := seedWork(t, m.db, definition, workSeed{subject: "closed", state: domain.WorkClosed, closeRequest: "close"}).ID
	homed := seedWork(t, m.db, definition, workSeed{subject: "homed", state: domain.WorkWorking, turns: 1, fileHome: domain.FileHome{Kind: "case", ID: "1"}}).ID
	m.start()
	changes := newInputChanges(m, slog.Default())

	changes.publish(uuid.NewString(), workInputs(stopped), added("brief.md"))
	changes.publish(uuid.NewString(), workInputs(closed), added("brief.md"))
	changes.publish(uuid.NewString(), &storageintegrationv1.OwnerFact{Condition: "file_home", Value: "case:1"}, added("brief.md"))

	require.Equal(t, []string{`Input file "brief.md" was added at 2026-11-20T08:30:00Z.`}, m.port.bufferedFor(stopped))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, m.state(stopped), "accepting a notice never starts the Work")
	require.Empty(t, m.port.bufferedFor(closed))
	require.Empty(t, m.port.bufferedFor(homed))
}

// A notice whose delivery keeps failing is repeated up to its ceiling; the
// attempt that exhausts it logs an error naming the Work.
func TestAnUndeliverableNoticeEndsAsAnObservableFailure(t *testing.T) {
	m := newLifecycleModule(t)
	stopped := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "stopped", state: domain.WorkStopped}).ID
	m.start()
	var logs bytes.Buffer
	changes := newInputChanges(m, slog.New(slog.NewJSONHandler(&logs, nil)))
	m.port.inputError = errors.New("dispatcher unavailable")

	change := added("brief.md")
	change.EventId, change.OccurredAt = uuid.NewString(), timestamppb.New(noticeTime)
	change.Directory = &storageintegrationv1.Directory{Owner: []*storageintegrationv1.OwnerFact{workInputs(stopped)}, Name: "inputs"}
	fact, err := integrationmessage.New(integrationmessage.KindOf(change), change)
	require.NoError(t, err)
	require.NoError(t, changes.handler.Handle(m.ctx, fact))
	require.Len(t, changes.queued.tasks, 1)
	require.Equal(t, agenttask.InputChangeNoticeRetries, changes.queued.retries[0])
	task := changes.queued.tasks[0]

	require.Error(t, changes.deliver.Process(noticeAttempt(m.ctx, task, agenttask.InputChangeNoticeRetries-1), task), "a failure before the ceiling is retried")
	require.NotContains(t, logs.String(), "input change notice failed")
	require.Error(t, changes.deliver.Process(noticeAttempt(m.ctx, task, agenttask.InputChangeNoticeRetries), task))
	require.Contains(t, logs.String(), `"level":"ERROR","msg":"input change notice failed"`)
	require.Contains(t, logs.String(), `"work_id":"`+stopped+`"`)
}

func noticeAttempt(ctx context.Context, task taskqueue.Task, retried int) context.Context {
	return taskqueue.ContextWithExecutionInfo(ctx, taskqueue.NewExecutionInfo(
		taskqueue.WithExecutionTaskID(task.ID()), taskqueue.WithExecutionRetryCount(retried),
		taskqueue.WithExecutionMaxRetry(agenttask.InputChangeNoticeRetries)))
}
