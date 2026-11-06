//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/talgent-work/workbench/pkg/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

// Resume commits its Work change and request together. A concurrent closure or
// request claimed by another Work must roll back the new execution obligation.
func TestInterruptedResumeLeavesNothingHeld(t *testing.T) {
	for _, scenario := range []struct {
		name                                          string
		closed, foreignAdmission, foreignCancellation bool
	}{
		{name: "admitted"}, {name: "closed before commit", closed: true},
		{name: "request admitted for another Work", foreignAdmission: true},
		{name: "request canceled for another Work", foreignCancellation: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f, id := stoppedContinuationSession(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			db, agents, sessions := f.db, f.agents, f.sessions
			auth := infrastructure.NewWorkAuthorizer(db)
			before, err := sessions.Get(ctx, id)
			require.NoError(t, err)
			var starts atomic.Int32
			factory := infrastructure.NewExecutionRequestsFactory(startupExecutionPort{onStart: func(domain.StartExecutionRequest) error { starts.Add(1); return nil }})
			barrier := newCommitBarrier(sessions)
			requestID := uuid.NewString()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(barrier.proceed) }) }
			result := make(chan error, 1)
			go func() {
				err := command.NewResumeWorkHandler(time.Now, sessions, agents, auth, computertest.Checked(startupAuthority{}), factory, barrier, infrastructure.NewContinuationRequests(db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: requestID})
				result <- err
			}()
			t.Cleanup(release)
			select {
			case <-barrier.arrived:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if scenario.closed {
				require.NoError(t, command.NewCloseWorkHandler(sessions, auth, factory, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: "close-before-admission"}))
			}
			var foreignID string
			var expectedStarts int32
			if scenario.foreignAdmission || scenario.foreignCancellation {
				foreign, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"other-owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "other-home", Namespace: "personal"}})
				require.NoError(t, err)
				require.NoError(t, agents.Save(ctx, foreign))
				foreignRequest := uuid.NewString()
				foreignFactory := infrastructure.NewExecutionRequestsFactory(executionPort{})
				if scenario.foreignAdmission {
					foreignRequest, foreignFactory = requestID, factory
					expectedStarts = 1
				}
				foreignID, err = command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, auth, creationModels(), &creationCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), foreignFactory).Handle(ctx, command.CreateWork{RequestID: foreignRequest, SubjectID: "other-owner", AgentID: foreign.ID, Subject: domain.WorkSubject{Kind: "case", ID: "other-work"}, ProjectID: "project", WorkspaceReference: "other-workspace"})
				require.NoError(t, err)
				if scenario.foreignCancellation {
					canceled, err := sessions.CancelStart(ctx, foreignID, requestID)
					require.NoError(t, err)
					require.True(t, canceled)
				}
			}
			release()
			startupErr := <-result
			switch {
			case scenario.closed:
				require.ErrorIs(t, startupErr, domain.ErrWorkTransition, "the retried admission observes the closed Work")
			case scenario.foreignAdmission || scenario.foreignCancellation:
				require.ErrorIs(t, startupErr, command.ErrStartRequestConflict)
			default:
				require.NoError(t, startupErr)
				expectedStarts = 1
			}
			require.Equal(t, expectedStarts, starts.Load(), "only a committed admission may execute")
			saved, err := sessions.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, !scenario.closed && !scenario.foreignAdmission && !scenario.foreignCancellation, saved.HoldsProcess())
			if scenario.foreignAdmission {
				foreignSession, err := sessions.Get(ctx, foreignID)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStarting, foreignSession.State)
			}
			ids, err := infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
			require.NoError(t, err)
			if scenario.closed {
				require.NotContains(t, ids, id, "a closed Work leaves recovery; Storage owns its workspace reclamation")
			} else if expectedStarts == 0 || scenario.foreignAdmission {
				require.NotContains(t, ids, id)
			}
			if saved.HoldsProcess() {
				return
			}
			if !scenario.closed {
				require.Equal(t, domain.WorkStopped, saved.State)
				require.Equal(t, before.HarnessProcess, saved.HarnessProcess, "failed admission must preserve the completed process")
			}
			create := command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, auth, creationModels(), &creationCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), factory)
			_, err = create.Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), SubjectID: "owner", AgentID: before.AgentID, Subject: domain.WorkSubject{Kind: "case", ID: "next-case"}, ProjectID: "project", WorkspaceReference: "next-workspace"})
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
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Original", RequiredPlugins: []string{"review"}, StartupAgent: "review:architect"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, agent))
	unavailable := errors.New("delivery interrupted")
	port := &recoveryExecutionPort{err: unavailable}
	originalRequest := uuid.Must(uuid.NewV7()).String()
	sessionID, err := command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, infrastructure.NewWorkAuthorizer(db), creationModels(), &repositoryCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(port)).Handle(ctx, command.CreateWork{RequestID: originalRequest, SubjectID: "owner", AgentID: agent.ID, Subject: domain.WorkSubject{Kind: "case", ID: "work"}, ProjectID: "project", WorkspaceReference: "work"})
	require.ErrorIs(t, err, unavailable)
	require.Len(t, port.starts, 1)
	original := port.starts[0]
	require.Equal(t, domain.ContextFresh, original.ContextMode)
	reconnect := func() {
		require.NoError(t, db.Close())
		db, err = xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		agents = infrastructure.NewAgentDefinitionRepository(db, time.Now)
		sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
	}
	recover := func() {
		t.Helper()
		reconnect()
		port.err = nil
		ids, err := infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
		require.NoError(t, err)
		require.Equal(t, []string{sessionID}, ids)
		require.NoError(t, command.NewRecoverWorkHandler(time.Now, sessions, infrastructure.NewExecutionRequestsFactory(port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, nil).Handle(ctx, sessionID))
	}
	recover()
	require.Equal(t, []domain.StartExecutionRequest{original, original}, port.starts)
	feedback := func(kind command.ExecutionFeedbackKind, _ string) error {
		observation := observedProcess{State: domain.ProcessRunning}
		if kind == command.ComputeReclaimed {
			observation = observedProcess{State: domain.ProcessPending, ComputeReclaimed: true}
		}
		return command.NewApplyExecutionFeedbackHandler(time.Now, sessions, observation, infrastructure.NewExecutionRequestsFactory(port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{}).Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: sessionID})
	}
	require.NoError(t, feedback(command.ExecutorPrepared, ""))
	require.NoError(t, feedback(command.AgentStarted, ""))
	port.err = unavailable
	require.ErrorIs(t, command.NewStopWorkHandler(sessions, infrastructure.NewWorkAuthorizer(db), infrastructure.NewExecutionRequestsFactory(port)).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: sessionID, RequestID: "stop"}), unavailable)
	recover()
	require.Equal(t, []string{"stop", "stop"}, port.stops)
	require.Len(t, port.starts, 2, "prepared execution must not be started while retrying stop")
	port.err = unavailable
	require.ErrorIs(t, feedback(command.ExecutionExited, "stop"), unavailable)
	recover()
	require.Equal(t, []string{sessionID, sessionID}, port.reclaims)
	require.NoError(t, feedback(command.ComputeReclaimed, ""))
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
	port.err = unavailable
	resumeRequest := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessionID}
	err = command.NewResumeWorkHandler(time.Now, sessions, agents, infrastructure.NewWorkAuthorizer(db), computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(port), sessions, infrastructure.NewContinuationRequests(db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, resumeRequest)
	require.ErrorIs(t, err, unavailable)
	beforeReplay, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	startCalls := len(port.starts)
	require.NoError(t, command.NewResumeWorkHandler(time.Now, sessions, agents, infrastructure.NewWorkAuthorizer(db), computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(port), sessions, infrastructure.NewContinuationRequests(db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, resumeRequest))
	afterReplay, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, beforeReplay, afterReplay)
	require.Len(t, port.starts, startCalls, "a replay cannot submit a second execution")
	continuation := port.starts[len(port.starts)-1]
	require.Equal(t, sessionID, continuation.WorkID)
	require.Equal(t, domain.ContextContinue, continuation.ContextMode)
	require.Equal(t, original.HarnessSessionReference, continuation.HarnessSessionReference)
	closeWork := func() *command.CloseWorkHandler {
		return command.NewCloseWorkHandler(sessions, infrastructure.NewWorkAuthorizer(db), infrastructure.NewExecutionRequestsFactory(port), commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{})
	}
	closeRequest := command.WorkControl{SubjectID: "owner", WorkID: sessionID, RequestID: "close"}
	require.ErrorIs(t, closeWork().Handle(ctx, closeRequest), domain.ErrWorkTransition, "a starting Work cannot be closed")
	recover()
	require.Equal(t, continuation, port.starts[len(port.starts)-1])
	require.NoError(t, feedback(command.ExecutorPrepared, ""))
	require.NoError(t, feedback(command.AgentStarted, ""))
	// A running Work is stopped first and keeps the close request.
	require.NoError(t, closeWork().Handle(ctx, closeRequest))
	require.Equal(t, []string{"stop", "stop", "close"}, port.stops)
	stopping, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, stopping.State)
	require.Equal(t, domain.ContextContinue, stopping.HarnessProcess.ContextMode)
	require.NoError(t, feedback(command.ExecutionExited, "close"))
	require.NoError(t, feedback(command.ComputeReclaimed, ""))
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
	startCalls = len(port.starts)
	require.NoError(t, command.NewResumeWorkHandler(time.Now, sessions, agents, infrastructure.NewWorkAuthorizer(db), computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(port), sessions, infrastructure.NewContinuationRequests(db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, resumeRequest), "historical request replay returns its original acceptance")
	require.Len(t, port.starts, startCalls)
	closedAfterReplay, err := sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, closed, closedAfterReplay, "replaying a historical acceptance cannot reopen a closed Session")
}
