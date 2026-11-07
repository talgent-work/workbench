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
	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/contracts/gen/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentrpc "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"xorm.io/xorm"
)

// These tests use the real Agent RPC, application, Work repository and MySQL.
// Only catalog/capacity/native delivery are controlled external boundaries.
func TestCancelWorkRegistrationFencesLateCreation(t *testing.T) {
	db, definitions, works, agent := startingCreationDatabase(t)
	execution := &creationExecution{db: db}
	paused := &pausedRegistration{WorkRegistrar: works, reached: make(chan struct{}), release: make(chan struct{})}
	create := command.NewCreateWorkHandler(time.Now, definitions, paused, works, infrastructure.NewWorkAuthorizer(db), creationModels(), &creationCatalog{}, creationConnections{}, works, computertest.Checked(&initialCreationCapacity{}), infrastructure.NewExecutionRequestsFactory(execution))
	request := creationInput(agent.ID, "canceled")
	request.InitialExecution = &command.PrepareWorkExecution{RequestID: request.RequestID, WorkBranch: "talgent/" + "TAL-368", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "original input"}}
	result := make(chan error, 1)
	go func() { _, err := create.Handle(t.Context(), request); result <- err }()
	select {
	case <-paused.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("creation did not reach registration")
	}
	// Ensure the goroutine is released even if a preceding assertion fails.
	released := false
	defer func() {
		if !released {
			close(paused.release)
		}
	}()
	client := registrationCancellationClient(t, db, works)
	canceled, err := client.CancelWorkRegistration(cancellationRPC(t.Context(), request, "control"))
	require.NoError(t, err)
	require.Empty(t, canceled.WorkId)
	close(paused.release)
	released = true
	require.ErrorIs(t, <-result, command.ErrStartRequestCanceled)
	require.Zero(t, execution.starts.Load())
	for _, table := range []string{"agent_session", "agent_initial_execution_request"} {
		var rows []int
		require.NoError(t, db.SQL("SELECT COUNT(*) FROM "+table).Find(&rows))
		require.Equal(t, []int{0}, rows, "cancellation must roll back Work and initial input together")
	}
	_, found, err := works.FindAdmittedStart(t.Context(), request.RequestID)
	require.NoError(t, err)
	require.False(t, found)
	// A reconstructed adapter and connection replay the lost cancellation reply.
	reopened, err := xorm.NewEngine("mysql", db.DataSourceName())
	require.NoError(t, err)
	defer reopened.Close()
	replay := registrationCancellationClient(t, reopened, infrastructure.NewAgentWorkRepository(reopened, time.Now))
	repeated, err := replay.CancelWorkRegistration(cancellationRPC(t.Context(), request, "control"))
	require.NoError(t, err)
	require.Empty(t, repeated.WorkId)
	for _, mutation := range []func(*command.CreateWork){
		func(r *command.CreateWork) { r.SubjectID = "intruder" },
		func(r *command.CreateWork) { r.AgentID = uuid.NewString() },
		func(r *command.CreateWork) { r.WorkspaceReference = "elsewhere" },
		func(r *command.CreateWork) {
			r.Subject = domain.WorkSubject{Kind: "case", ID: "elsewhere"}
		},
	} {
		different := request
		mutation(&different)
		_, err := replay.CancelWorkRegistration(cancellationRPC(t.Context(), different, "control"))
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	}
	// A new explicit request identity remains eligible; no auto retry was queued.
	request.RequestID = uuid.NewString()
	request.InitialExecution.RequestID = request.RequestID
	fresh := command.NewCreateWorkHandler(time.Now, definitions, works, works, infrastructure.NewWorkAuthorizer(db), creationModels(), &creationCatalog{}, creationConnections{}, works, computertest.Checked(&initialCreationCapacity{}), infrastructure.NewExecutionRequestsFactory(execution))
	id, err := fresh.Handle(t.Context(), request)
	require.NoError(t, err)
	work, err := works.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, work.State)
}

func TestCancelWorkRegistrationRecoversCommittedCreation(t *testing.T) {
	db, definitions, works, agent := startingCreationDatabase(t)
	execution := &creationExecution{db: db}
	lost := errors.New("registration response lost after commit")
	registrar := &lostRegistrationReply{WorkRegistrar: works, err: lost}
	create := command.NewCreateWorkHandler(time.Now, definitions, registrar, works, infrastructure.NewWorkAuthorizer(db), creationModels(), &creationCatalog{}, creationConnections{}, works, computertest.Checked(&initialCreationCapacity{}), infrastructure.NewExecutionRequestsFactory(execution))
	request := creationInput(agent.ID, "committed")
	id, err := create.Handle(t.Context(), request)
	require.ErrorIs(t, err, lost)
	require.Empty(t, id)
	admitted, found, err := works.FindAdmittedStart(t.Context(), request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	original, err := works.Get(t.Context(), admitted.WorkID)
	require.NoError(t, err)
	client := registrationCancellationClient(t, db, works)
	for range 2 {
		reply, err := client.CancelWorkRegistration(cancellationRPC(t.Context(), request, "control"))
		require.NoError(t, err)
		require.Equal(t, original.ID, reply.WorkId)
	}
	after, err := works.Get(t.Context(), original.ID)
	require.NoError(t, err)
	require.Equal(t, original, after, "lookup of an admitted request must not cancel or rewrite its Work")
	require.Zero(t, execution.starts.Load(), "the creation delivery failed; cancellation does not start it")
	unauthorized := request
	unauthorized.SubjectID = "intruder"
	_, err = client.CancelWorkRegistration(cancellationRPC(t.Context(), unauthorized, "control"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	wrong := request
	wrong.WorkspaceReference = "wrong"
	_, err = client.CancelWorkRegistration(cancellationRPC(t.Context(), wrong, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	_, err = client.CancelWorkRegistration(cancellationRPC(t.Context(), request, "wrong-token"))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func registrationCancellationClient(t *testing.T, db *xorm.Engine, works *infrastructure.AgentWorkRepository) agentprivatev1connect.AgentServiceClient {
	t.Helper()
	app := &application.Application{RegistrationCancellation: works, WorkPrincipals: infrastructure.NewWorkAuthorizer(db), Works: works}
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterAgentServiceHandler(s, agentrpc.NewAgentHandler(app, "control"))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
}

// cancellationRPC is the cancellation of request under token, and the
// context of its call.
func cancellationRPC(ctx context.Context, request command.CreateWork, token string) (context.Context, *agentprivatev1.CancelWorkRegistrationRequest) {
	rpc := &agentprivatev1.CancelWorkRegistrationRequest{RequestId: request.RequestID, SubjectId: request.SubjectID, AgentId: request.AgentID, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: request.Subject.ID}, ProjectId: request.ProjectID, WorkspaceReference: request.WorkspaceReference}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx, rpc
}

type pausedRegistration struct {
	command.WorkRegistrar
	reached, release chan struct{}
}

func (p *pausedRegistration) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	close(p.reached)
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.WorkRegistrar.RegisterWork(ctx, work, registration)
}

type lostRegistrationReply struct {
	command.WorkRegistrar
	err error
}

func (p *lostRegistrationReply) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	if err := p.WorkRegistrar.RegisterWork(ctx, work, registration); err != nil {
		return err
	}
	return p.err
}
