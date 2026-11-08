//go:build integration

package infrastructure_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

// commitBarrier pauses a real start admission just before it commits, so a
// concurrent change of the same Work races the admission transaction.
type commitBarrier struct {
	*infrastructure.AgentWorkRepository
	arrived, proceed chan struct{}
	once             *sync.Once
}

func newCommitBarrier(sessions *infrastructure.AgentWorkRepository) commitBarrier {
	return commitBarrier{AgentWorkRepository: sessions, arrived: make(chan struct{}), proceed: make(chan struct{}), once: &sync.Once{}}
}

func (b commitBarrier) Save(ctx context.Context, work *domain.AgentWork) error {
	b.once.Do(func() { close(b.arrived) })
	select {
	case <-b.proceed:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.AgentWorkRepository.Save(ctx, work)
}

// Resume commits its Work change alone. A concurrent closure must roll back
// the new execution obligation.
func TestInterruptedResumeLeavesNothingHeld(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		closed bool
	}{
		{name: "admitted"}, {name: "closed before commit", closed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f, id := stoppedContinuationSession(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			db, agents, sessions := f.db, f.agents, f.sessions
			auth := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners)
			before, err := sessions.Get(ctx, id)
			require.NoError(t, err)
			var starts atomic.Int32
			counted := func(works domain.AgentWorkRepository) startedWorks {
				return startedWorks{AgentWorkRepository: works, onStart: func(domain.LaunchSpecification) { starts.Add(1) }}
			}
			barrier := newCommitBarrier(sessions)
			requestID := uuid.NewString()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(barrier.proceed) }) }
			result := make(chan error, 1)
			go func() {
				err := command.NewResumeWorkHandler(time.Now, counted(barrier), agents, auth, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: requestID})
				result <- err
			}()
			t.Cleanup(release)
			select {
			case <-barrier.arrived:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if scenario.closed {
				require.NoError(t, command.NewCloseWorkHandler(sessions, auth, executionsOf(db), &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: "close-before-admission"}))
			}
			var expectedStarts int32
			release()
			startupErr := <-result
			switch {
			case scenario.closed:
				require.ErrorIs(t, startupErr, domain.ErrWorkTransition, "the retried admission observes the closed Work")
			default:
				require.NoError(t, startupErr)
				expectedStarts = 1
			}
			require.Equal(t, expectedStarts, starts.Load(), "only a committed admission may execute")
			saved, err := sessions.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, !scenario.closed, saved.HoldsProcess())
			ids, err := infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
			require.NoError(t, err)
			if scenario.closed {
				require.NotContains(t, ids, id, "a closed Work leaves recovery; Storage owns its workspace reclamation")
			} else if expectedStarts == 0 {
				require.NotContains(t, ids, id)
			}
			if saved.HoldsProcess() {
				return
			}
			if !scenario.closed {
				require.Equal(t, domain.WorkStopped, saved.State)
				require.Equal(t, before.HarnessProcess, saved.HarnessProcess, "failed admission must preserve the completed process")
			}
			create := command.NewCreateWorkHandler(time.Now, agents, counted(sessions), auth, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(db, creationModels()))
			_, err = create.Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), SubjectID: "owner", AgentID: before.AgentID, Subject: domain.WorkSubject{Kind: "case", ID: "next-case"}, ProjectID: "project"})
			require.NoError(t, err, "an uncommitted admission holds no concurrency")
			require.Equal(t, expectedStarts+1, starts.Load())
		})
	}
}

func TestRecoveryReloadsStartupStopAndRemovalObligations(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Original", RequiredPlugins: []string{"review"}, StartupAgent: "review:architect"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, agent))
	run := newRuns(t, db, sessions)
	originalRequest := uuid.Must(uuid.NewV7()).String()
	sessionID, err := command.NewCreateWorkHandler(time.Now, agents, sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), creationModels(), commandtest.StartCatalog(&repositoryCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(db, creationModels())).Handle(ctx, command.CreateWork{RequestID: originalRequest, SubjectID: "owner", AgentID: agent.ID, Subject: domain.WorkSubject{Kind: "case", ID: "work"}, ProjectID: "project"})
	require.NoError(t, err)
	original := requireStartRequested(t, sessions, sessionID)
	require.Equal(t, domain.ContextFresh, original.ContextMode)
	// The admission's Domain Event is lost: no execution has begun.
	_, err = run.executions.Latest(ctx, sessionID)
	require.ErrorIs(t, err, domain.ErrExecutionNotFound)
	reconnect := func() {
		require.NoError(t, db.Close())
		db, err = xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		agents = infrastructure.NewAgentDefinitionRepository(db, time.Now)
		sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
		run = newRuns(t, db, sessions)
	}
	recover := func() {
		t.Helper()
		reconnect()
		ids, err := infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
		require.NoError(t, err)
		require.Equal(t, []string{sessionID}, ids)
		require.NoError(t, command.NewRecoverWorkHandler(time.Now, sessions, &commandtest.InputBuffer{}, command.NewInputAcceptance(sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil).Handle(ctx, sessionID))
	}
	recover()
	begun := run.latest(ctx, sessionID)
	require.Equal(t, original, launchOf(begun), "recovery passes the admitted start on")
	recover()
	require.Equal(t, begun.ID, run.latest(ctx, sessionID).ID, "a repeated recovery begins no second execution")
	run.ready(ctx, sessionID)
	require.NoError(t, command.NewStopWorkHandler(sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), executionsOf(db)).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: sessionID, RequestID: "stop"}))
	unsent, err := run.executions.Latest(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, unsent.ExitRequest.Requested(), "the stop's Domain Event is lost")
	recover()
	stopped := run.latest(ctx, sessionID)
	require.Equal(t, "stop", stopped.ExitRequest.StopRequestID, "recovery passes the admitted stop on")
	require.Equal(t, begun.ID, stopped.ID, "prepared execution must not be started while retrying stop")
	// The exit reaches the Work, but the release it asks for is lost.
	require.NoError(t, run.feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: sessionID}))
	exited, err := run.executions.Latest(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, exited.Retired())
	recover()
	require.True(t, run.latest(ctx, sessionID).Retired(), "recovery passes the release on")
	run.release(ctx, sessionID)
	completed, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, completed.State)
	require.False(t, completed.HoldsProcess())
	reconnect()
	ids, err := infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, ids, "a stopped Work has no remaining recovery obligation")

	// A close racing an interrupted Resume must retain the selected continuation
	// context while obtaining the resumed process's exit.
	resumeRequest := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessionID}
	require.NoError(t, command.NewResumeWorkHandler(time.Now, sessions, agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, resumeRequest))
	beforeReplay, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NoError(t, command.NewResumeWorkHandler(time.Now, sessions, agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, resumeRequest))
	afterReplay, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, beforeReplay, afterReplay, "a replay cannot admit a second execution")
	continuation := requireStartRequested(t, sessions, sessionID)
	require.Equal(t, sessionID, continuation.WorkID)
	require.Equal(t, domain.ContextContinue, continuation.ContextMode)
	require.Equal(t, original.HarnessSessionReference, continuation.HarnessSessionReference)
	closeWork := func() *command.CloseWorkHandler {
		return command.NewCloseWorkHandler(sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), executionsOf(db), &commandtest.InputBuffer{})
	}
	closeRequest := command.WorkControl{SubjectID: "owner", WorkID: sessionID, RequestID: "close"}
	require.ErrorIs(t, closeWork().Handle(ctx, closeRequest), domain.ErrWorkTransition, "a starting Work cannot be closed")
	recover()
	resumed := run.latest(ctx, sessionID)
	require.NotEqual(t, begun.ID, resumed.ID)
	require.Equal(t, continuation, launchOf(resumed), "recovery begins the continuation's execution")
	run.ready(ctx, sessionID)
	// A running Work is stopped first and keeps the close request.
	require.NoError(t, closeWork().Handle(ctx, closeRequest))
	require.Equal(t, "close", run.latest(ctx, sessionID).ExitRequest.StopRequestID)
	stopping, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, stopping.State)
	require.Equal(t, domain.ContextContinue, stopping.HarnessProcess.ContextMode)
	run.exit(ctx, sessionID)
	run.release(ctx, sessionID)
	reconnect()
	require.NoError(t, closeWork().ApplyKeptClose(ctx, sessionID))
	closed, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.True(t, closed.HarnessProcess.Exited)
	require.True(t, closed.HarnessProcess.ComputeReclaimed)
	ids, err = infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, ids, "a closed Work leaves recovery; Storage owns its workspace reclamation")
	require.ErrorIs(t, command.NewResumeWorkHandler(time.Now, sessions, agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, resumeRequest), domain.ErrWorkTransition, "a late repeat of an ended start starts nothing")
	require.Equal(t, resumed.ID, run.latest(ctx, sessionID).ID, "replaying a historical request begins no execution")
	closedAfterReplay, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, closed, closedAfterReplay, "replaying a historical request cannot reopen a closed Session")
}
