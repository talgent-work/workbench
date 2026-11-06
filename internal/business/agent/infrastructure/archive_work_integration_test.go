//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agenttransport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func TestExecutionArchiveRPCRequiresStoppedSessionWithoutPendingContinuation(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	port := &recoveryExecutionPort{}
	factory := infrastructure.NewExecutionRequestsFactory(port)
	authorization := infrastructure.NewWorkAuthorizer(f.db)
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	handler := command.NewCloseWorkHandler(f.sessions, authorization, factory, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, receipts, &commandtest.InputBuffer{})
	app := &application.Application{Works: f.sessions, Commands: application.Commands{CloseWork: handler}}
	_, endpoint := agentprivatev1connect.NewAgentServiceHandler(agenttransport.NewAgentHandler(app, "control"))
	server := httptest.NewServer(endpoint)
	t.Cleanup(server.Close)
	client := agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL)
	archive := func(subject, token, sessionID string) error {
		r := connect.NewRequest(&agentprivatev1.CloseWorkRequest{SubjectId: subject, WorkId: sessionID, RequestId: uuid.NewString()})
		r.Header().Set("Authorization", "Bearer "+token)
		_, err := client.CloseWork(ctx, r)
		return err
	}
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(archive("other", "control", id)))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(archive("owner", "wrong", id)))
	request := command.ContinuationRequest{StartWork: command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}, Operation: "resume"}
	_, err = receipts.RetainContinuation(ctx, request, before.Version)
	require.NoError(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(archive("owner", "control", id)))
	stillStopped, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, stillStopped.State)
	require.Equal(t, before.Version+1, stillStopped.Version, "rejected archive must not save another Session revision")
	require.Empty(t, port.stops)
	require.Empty(t, port.reclaims)
	require.NoError(t, receipts.RecordContinuationRejection(ctx, id, request.RequestID, "capacity"))
	require.NoError(t, receipts.ResolveContinuation(ctx, id, request.RequestID, false))
	require.NoError(t, archive("owner", "control", id))
	require.NoError(t, archive("owner", "control", id), "closed archive remains idempotent")
	// A caller's completion closing an already closed Work is acknowledged
	// under its own request, so the completion settles.
	completion := connect.NewRequest(&agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "case-completion"})
	completion.Header().Set("Authorization", "Bearer control")
	acknowledged, err := client.CloseWork(ctx, completion)
	require.NoError(t, err)
	require.Equal(t, "case-completion", acknowledged.Msg.RequestId)
	closed, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, before.WorkspaceReference, closed.WorkspaceReference)
	require.Equal(t, before.HarnessProcess, closed.HarnessProcess, "archive keeps the last process facts")
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, authorization, query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), factory)
	created, err := create.Handle(ctx, command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: before.ProjectID, RequestID: uuid.NewString(), SubjectID: "owner", AgentID: before.AgentID, WorkspaceReference: uuid.NewString()})
	require.NoError(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(archive("owner", "control", created)), "a newly admitted Starting Work cannot be archived")
	ref := created
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(archive("owner", "control", created)))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	require.NoError(t, archive("owner", "control", created), "a running Work is stopped before it closes")
	stopping, err := f.sessions.Get(ctx, created)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, stopping.State)
	require.NotEmpty(t, stopping.CloseRequestReference, "the close applies once the Work has stopped")
	require.Len(t, port.stops, 1)
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutionExited, command.ComputeReclaimed} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
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

type archiveContinuationRead struct {
	query.ContinuationReader
	after func() error
}

func (r archiveContinuationRead) GetWorkContinuation(ctx context.Context, id string) (query.WorkContinuation, error) {
	value, err := r.ContinuationReader.GetWorkContinuation(ctx, id)
	if err == nil {
		err = r.after()
	}
	return value, err
}

func TestExecutionArchiveAndContinuationCannotBothCommitFromStoppedRevision(t *testing.T) {
	for _, continuationFirst := range []bool{true, false} {
		name := "archive commits first"
		if continuationFirst {
			name = "continuation retained after archive read"
		}
		t.Run(name, func(t *testing.T) {
			f, id := stoppedContinuationSession(t)
			ctx := context.Background()
			other, err := xorm.NewEngine("mysql", f.dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = other.Close() })
			before, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			receipts := infrastructure.NewContinuationRequests(other, time.Now)
			request := command.ContinuationRequest{StartWork: command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}, Operation: "resume"}
			var reader query.ContinuationReader = receipts
			if continuationFirst {
				reader = archiveContinuationRead{ContinuationReader: receipts, after: func() error {
					_, err := receipts.RetainContinuation(ctx, request, before.Version)
					return err
				}}
			}
			port := &recoveryExecutionPort{}
			factory := infrastructure.NewExecutionRequestsFactory(port)
			authorization := infrastructure.NewWorkAuthorizer(f.db)
			archive := command.NewCloseWorkHandler(f.sessions, authorization, factory, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, reader, &commandtest.InputBuffer{})
			err = archive.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()})
			observer := infrastructure.NewAgentWorkRepository(other, time.Now)
			if continuationFirst {
				require.ErrorIs(t, err, domain.ErrConcurrentModification)
				stored, err := observer.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStopped, stored.State)
				err = command.NewResumeWorkHandler(time.Now, observer, f.agents, authorization, computertest.Checked(startupAuthority{}), factory, observer, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, request.StartWork)
				require.NoError(t, err)
				stored, err = observer.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStarting, stored.State)
			} else {
				require.NoError(t, err)
				_, err = receipts.RetainContinuation(ctx, request, before.Version)
				require.ErrorIs(t, err, domain.ErrConcurrentModification)
				err = command.NewResumeWorkHandler(time.Now, observer, f.agents, authorization, computertest.Checked(startupAuthority{}), factory, observer, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, request.StartWork)
				require.ErrorIs(t, err, domain.ErrWorkTransition)
				stored, err := observer.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkClosed, stored.State)
				require.Empty(t, port.starts)
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
	port := &recoveryExecutionPort{}
	factory := infrastructure.NewExecutionRequestsFactory(port)
	authorization := infrastructure.NewWorkAuthorizer(f.db)
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	ref, err := continuedRef(id, command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, authorization, computertest.Checked(startupAuthority{}), factory, f.sessions, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	// Startup failure established by Agent before the process was prepared.
	failed, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, failed.RecordFailure("runtime unavailable"))
	require.NoError(t, f.sessions.Save(ctx, failed))
	lost := errors.New("archive save acknowledgement lost")
	requestID := uuid.NewString()
	err = command.NewCloseWorkHandler(archiveLostSave{f.sessions, lost}, authorization, factory, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, receipts, &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: requestID})
	require.ErrorIs(t, err, lost)
	require.Empty(t, port.stops, "lost save acknowledgement prevents immediate control send")
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	sessions := infrastructure.NewAgentWorkRepository(other, time.Now)
	closing, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosing, closing.State)
	require.Equal(t, requestID, closing.StopRequestReference)
	recovery := command.NewRecoverWorkHandler(time.Now, sessions, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, nil)
	require.NoError(t, recovery.Handle(ctx, id))
	require.Equal(t, []string{requestID}, port.stops)
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutionExited, command.ComputeReclaimed} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	closed, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, "runtime unavailable (exit code 0)", closed.FailureReason, "the exit fact adds its code to the recorded failure")
	require.Len(t, port.starts, 2, "an unprepared admitted attempt must be delivered before its correlated stop")
	require.Equal(t, port.starts[0], port.starts[1], "recovery replays the same admitted execution, context and bindings")
	require.Equal(t, ref, port.starts[1].WorkID)
}
