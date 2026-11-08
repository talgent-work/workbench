//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agenttransport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"xorm.io/xorm"
)

func TestExecutionArchiveRPCRequiresAStoppedWork(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	run := newRuns(t, f.db, f.sessions)
	stoppedRun := run.latest(ctx, id)
	authorization := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners)
	handler := command.NewCloseWorkHandler(f.sessions, authorization, run.executions, &commandtest.InputBuffer{})
	app := &application.Application{Works: f.sessions, Commands: application.Commands{CloseWork: handler}}
	endpoint := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterAgentServiceHandler(s, agenttransport.NewAgentHandler(app, "control"))
	})
	server := httptest.NewServer(endpoint)
	t.Cleanup(server.Close)
	client := agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	archive := func(subject, token, sessionID string) error {
		r := &agentprivatev1.CloseWorkRequest{SubjectId: subject, WorkId: sessionID, RequestId: uuid.NewString()}
		ctx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set("Authorization", "Bearer "+token)
		_, err := client.CloseWork(ctx, r)
		return err
	}
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(archive("other", "control", id)))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(archive("owner", "wrong", id)))
	stillStopped, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, stillStopped.State)
	require.Equal(t, before.Version, stillStopped.Version, "rejected archive must not save another Session revision")
	require.Equal(t, stoppedRun, run.latest(ctx, id), "rejected archive asks no execution to exit or release")
	require.NoError(t, archive("owner", "control", id))
	require.NoError(t, archive("owner", "control", id), "closed archive remains idempotent")
	// A caller's completion closing an already closed Work is acknowledged
	// under its own request, so the completion settles.
	completion := &agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "case-completion"}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer control")
	acknowledged, err := client.CloseWork(ctx, completion)
	require.NoError(t, err)
	require.Equal(t, "case-completion", acknowledged.RequestId)
	closed, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, before.LocatedLayout, closed.LocatedLayout)
	require.Equal(t, before.HarnessProcess, closed.HarnessProcess, "archive keeps the last process facts")
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, authorization, query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
	created, err := create.Handle(ctx, command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: before.ProjectID, RequestID: uuid.NewString(), SubjectID: "owner", AgentID: before.AgentID})
	require.NoError(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(archive("owner", "control", created)), "a newly admitted Starting Work cannot be archived")
	ref := created
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(archive("owner", "control", created)))
		require.NoError(t, run.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	require.NoError(t, archive("owner", "control", created), "a running Work is stopped before it closes")
	stopping, err := f.sessions.Get(ctx, created)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, stopping.State)
	require.NotEmpty(t, stopping.CloseRequestReference, "the close applies once the Work has stopped")
	require.Equal(t, stopping.StopRequestReference, run.latest(ctx, ref).ExitRequest.StopRequestID, "the stop asks the running process to exit")
	run.exit(ctx, ref)
	run.release(ctx, ref)
	stopped, err := f.sessions.Get(ctx, created)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, stopped.State)
	ids, err := infrastructure.NewExecutionQueryRepository(f.db).ListWorkIDs(ctx, "", 1000)
	require.NoError(t, err)
	require.Contains(t, ids, created, "recovery finds a stopped Work that kept a close")
	require.NoError(t, handler.ApplyKeptClose(ctx, created))
	ids, err = infrastructure.NewExecutionQueryRepository(f.db).ListWorkIDs(ctx, "", 1000)
	require.NoError(t, err)
	closedNow, err := f.sessions.Get(ctx, created)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closedNow.State)
	require.NotContains(t, ids, created, "a closed Work leaves recovery; Storage owns its workspace reclamation")
}

// workReadBarrier runs after once the first read of a Work returns, before
// its reader decides; no sleeps or fake SQL state.
type workReadBarrier struct {
	domain.AgentWorkRepository
	after func()
	done  *bool
}

func (r workReadBarrier) Get(ctx context.Context, id string) (*domain.AgentWork, error) {
	work, err := r.AgentWorkRepository.Get(ctx, id)
	if err == nil && !*r.done {
		*r.done = true
		r.after()
	}
	return work, err
}

func TestExecutionArchiveAndResumeCannotBothCommitFromStoppedRevision(t *testing.T) {
	for _, resumeFirst := range []bool{true, false} {
		name := "archive commits first"
		if resumeFirst {
			name = "resume commits after archive read"
		}
		t.Run(name, func(t *testing.T) {
			f, id := stoppedContinuationSession(t)
			ctx := context.Background()
			other, err := xorm.NewEngine("mysql", f.dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = other.Close() })
			observer := infrastructure.NewAgentWorkRepository(other, time.Now)
			run := newRuns(t, f.db, f.sessions)
			stoppedRun := run.latest(ctx, id)
			authorization := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners)
			resume := command.NewResumeWorkHandler(time.Now, observer, f.agents, authorization, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
			request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			var works domain.AgentWorkRepository = f.sessions
			if resumeFirst {
				works = workReadBarrier{AgentWorkRepository: f.sessions, done: new(bool), after: func() { require.NoError(t, resume.Handle(ctx, request)) }}
			}
			archive := command.NewCloseWorkHandler(works, authorization, run.executions, &commandtest.InputBuffer{})
			err = archive.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()})
			if resumeFirst {
				require.ErrorIs(t, err, domain.ErrConcurrentModification)
				stored, err := observer.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStarting, stored.State)
			} else {
				require.NoError(t, err)
				require.ErrorIs(t, resume.Handle(ctx, request), domain.ErrWorkTransition)
				stored, err := observer.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkClosed, stored.State)
				require.Equal(t, stoppedRun, run.latest(ctx, id), "a closed Work begins no execution")
			}
		})
	}
}

type archiveLostSave struct {
	domain.AgentWorkRepository
	lost error
}

func (r archiveLostSave) Save(ctx context.Context, session *domain.AgentWork) error {
	if err := r.AgentWorkRepository.Save(ctx, session); err != nil {
		return err
	}
	return r.lost
}

func TestExecutionArchiveRecoversCloseFromAgentAfterCommitAcknowledgementLoss(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	run := newRuns(t, f.db, f.sessions)
	authorization := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners)
	ref, err := continuedRef(id, command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, authorization, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	admitted := run.latest(ctx, ref)
	require.NotNil(t, admitted, "the admitted start begins its execution")
	run.launch(ctx, ref)
	// Startup failure established by Agent before the process was prepared.
	failed, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, failed.RecordFailure("runtime unavailable"))
	require.NoError(t, f.sessions.Save(ctx, failed))
	lost := errors.New("archive save acknowledgement lost")
	requestID := uuid.NewString()
	err = command.NewCloseWorkHandler(archiveLostSave{f.sessions, lost}, authorization, run.executions, &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: requestID})
	require.ErrorIs(t, err, lost)
	unsent, err := run.executions.Latest(ctx, ref)
	require.NoError(t, err)
	require.False(t, unsent.ExitRequest.Requested(), "lost save acknowledgement prevents immediate control send")
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	sessions := infrastructure.NewAgentWorkRepository(other, time.Now)
	closing, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosing, closing.State)
	require.Equal(t, requestID, closing.StopRequestReference)
	recovery := command.NewRecoverWorkHandler(time.Now, sessions, &commandtest.InputBuffer{}, command.NewInputAcceptance(sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil)
	require.NoError(t, recovery.Handle(ctx, id))
	stopped := newRuns(t, other, sessions)
	asked := stopped.latest(ctx, ref)
	require.Equal(t, requestID, asked.ExitRequest.StopRequestID, "recovery passes the retained close's stop on")
	require.Equal(t, admitted.ID, asked.ID, "the stop reaches the same admitted execution, not a new one")
	stopped.exit(ctx, ref)
	stopped.release(ctx, ref)
	closed, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, "runtime unavailable (exit code 0)", closed.FailureReason, "the exit fact adds its code to the recorded failure")
	require.Equal(t, admitted.Launch, asked.Launch, "the stopped execution keeps the admitted context and bindings")
}
