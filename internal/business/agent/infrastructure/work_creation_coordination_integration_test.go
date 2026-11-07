//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

type creationCapacity struct{ err error }

func (c *creationCapacity) AdmitRoom(context.Context) error { return c.err }

// creationCoordinator uses production coordination, registration, preparation
// and recovery storage; only execution delivery and catalogs are fixtures.
func creationCoordinator(db *xorm.Engine, records command.WorkCreationRequests, capacity computertest.RoomCheck, resources *registrationResources, starts *[]domain.StartExecutionRequest, registrar command.AgentWorkRepository, options agentquery.ModelConfigurations, catalog commandtest.PluginCatalog) (*command.CreateRequestedWorkHandler, *command.RecoverInitialExecutionHandler) {
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	if registrar == nil {
		registrar = sessions
	}
	initial := infrastructure.NewInitialExecutionRequests(db, time.Now)
	execution := startupExecutionPort{onStart: func(r domain.StartExecutionRequest) error { *starts = append(*starts, r); return nil }}
	create := command.NewCreateWorkHandler(time.Now, agents, registrar, sessions, auth, options, commandtest.StartCatalog(catalog, creationConnections{}), computertest.Checked(capacity), execution)
	prepare := command.NewPrepareWorkExecutionHandler(initial, sessions, auth, execution, records, options)
	return command.NewCreateRequestedWorkHandler(records, create, prepare), command.NewRecoverInitialExecutionHandler(initial, sessions, prepare, resources, execution)
}

func TestWorkCreationCoordinatorRecoversOriginalRegisteredSession(t *testing.T) {
	for _, lostAt := range []string{"registration"} {
		t.Run(lostAt, func(t *testing.T) {
			f := newWorkCreationFixture(t)
			ctx := context.Background()
			lost := errors.New("acknowledgement lost")
			registrar := registrationLostAcknowledgement{AgentWorkRepository: f.sessions, err: lost}
			resources := &registrationResources{}
			var starts []domain.StartExecutionRequest
			handler, _ := creationCoordinator(f.db, f.requests, startupAuthority{}, resources, &starts, registrar, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{})
			_, err := handler.Handle(ctx, f.request.Creation)
			require.ErrorIs(t, err, lost)
			retained, err := f.requests.GetWorkCreation(ctx, f.request.ID)
			require.NoError(t, err)
			require.NotEmpty(t, retained.WorkID)
			require.False(t, retained.BindingConfirmed)
			require.Empty(t, starts)
			require.Empty(t, resources.inputs)
			require.NoError(t, f.db.Close())
			db, err := xorm.NewEngine("mysql", f.dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			records := infrastructure.NewWorkCreationRequests(db, time.Now)
			pending, err := records.ListPendingWorkCreations(ctx, "", 100)
			require.NoError(t, err)
			require.Equal(t, []string{f.request.ID}, pending)
			handler, recovery := creationCoordinator(db, records, startupAuthority{}, resources, &starts, nil, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{})
			require.NoError(t, handler.Recover(ctx, pending[0]))
			require.Len(t, starts, 1)
			require.Equal(t, retained.WorkID, starts[0].WorkID)
			admitted, found, err := infrastructure.NewAgentWorkRepository(db, time.Now).FindAdmittedStart(ctx, f.request.ID)
			require.NoError(t, err)
			require.True(t, found, "the original creation request is the admitted start request")
			require.Equal(t, retained.WorkID, admitted.WorkID)
			sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
			session, err := sessions.Get(ctx, retained.WorkID)
			require.NoError(t, err)
			completeCreationCheckout(t, db, session.ID)
			require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
			require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
			require.NoError(t, sessions.Save(ctx, session))
			require.NoError(t, recovery.Handle(ctx, session.ID))
			require.NoError(t, recovery.Handle(ctx, session.ID))
			require.Equal(t, []command.ExecutionInput{f.request.Creation.InitialExecution.Input}, resources.inputs)
			pending, err = records.ListPendingWorkCreations(ctx, "", 100)
			require.NoError(t, err)
			require.Empty(t, pending)
		})
	}
}

type confirmationLostAfterCommit struct {
	command.WorkCreationRequests
	err error
}

func (s confirmationLostAfterCommit) ConfirmWorkCreation(ctx context.Context, id, session string) error {
	if err := s.WorkCreationRequests.ConfirmWorkCreation(ctx, id, session); err != nil {
		return err
	}
	return s.err
}

// Rejected admission creates no Work. A new explicit request can be admitted;
// its original input is durable even when association confirmation loses its reply.
// Restored capacity cannot turn the canceled request into background admission.
func TestWorkCreationRetryHandoffSurvivesConfirmationCommit(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	capacity := &creationCapacity{err: domain.ErrCapacityUnavailable}
	resources := &registrationResources{}
	var starts []domain.StartExecutionRequest
	handler, _ := creationCoordinator(f.db, f.requests, capacity, resources, &starts, nil, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{})
	_, err := handler.Handle(ctx, f.request.Creation)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	first, err := f.requests.GetWorkCreation(ctx, f.request.ID)
	require.NoError(t, err)
	require.False(t, first.BindingConfirmed)
	require.Empty(t, first.WorkID)
	require.True(t, first.Canceled, "a definitive refusal fences the identity")
	actual, err := f.sessions.FindWork(ctx, first.Creation.AgentID, first.Creation.Subject)
	require.NoError(t, err)
	require.Nil(t, actual)
	pending, err := f.requests.ListPendingWorkCreations(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
	capacity.err = nil
	require.NoError(t, handler.Recover(ctx, first.ID), "recovery has nothing left to advance")
	_, err = handler.Handle(ctx, first.Creation)
	require.ErrorIs(t, err, command.ErrWorkCreationCanceled, "repeating a refused identity answers its original outcome")
	require.Empty(t, starts, "restored capacity cannot silently retry rejected admission")
	retry := first.Creation
	initial := *retry.InitialExecution
	initial.RequestID, initial.CreationRequestID, initial.Input.ID = uuid.NewString(), "", uuid.NewString()
	initial.Input.Text = "explicit retry after capacity rejection"
	retry.InitialExecution = &initial
	retry.RequestID = initial.RequestID
	lost := errors.New("Agent confirmation committed before handoff; response lost")
	handler, _ = creationCoordinator(f.db, confirmationLostAfterCommit{WorkCreationRequests: f.requests, err: lost}, capacity, resources, &starts, nil, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{})
	_, err = handler.Handle(ctx, retry)
	require.ErrorIs(t, err, lost)
	pending, err = f.requests.ListPendingWorkCreations(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{initial.RequestID}, pending)
	registered, err := f.requests.GetWorkCreation(ctx, initial.RequestID)
	require.NoError(t, err)
	require.NotEmpty(t, registered.WorkID)
	require.True(t, registered.BindingConfirmed)
	current, err := infrastructure.NewInitialExecutionRequests(f.db, time.Now).Get(ctx, registered.WorkID)
	require.NoError(t, err)
	require.Equal(t, initial.RequestID, current.RequestID, "input and first admission commit with the new Work")
	require.Equal(t, initial.Input, current.Input)
	require.True(t, current.Submitted)
	require.False(t, current.Rejected)
	require.Empty(t, starts)
	require.NoError(t, f.db.Close())
	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	records := infrastructure.NewWorkCreationRequests(db, time.Now)
	handler, recovery := creationCoordinator(db, records, capacity, resources, &starts, nil, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{})
	require.NoError(t, handler.Recover(ctx, initial.RequestID))
	require.Len(t, starts, 1)
	require.Equal(t, registered.WorkID, starts[0].WorkID)
	admitted, found, err := infrastructure.NewAgentWorkRepository(db, time.Now).FindAdmittedStart(ctx, initial.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, registered.WorkID, admitted.WorkID)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	session, err := sessions.Get(ctx, registered.WorkID)
	require.NoError(t, err)
	completeCreationCheckout(t, db, session.ID)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.Save(ctx, session))
	require.NoError(t, recovery.Handle(ctx, session.ID))
	require.Equal(t, []command.ExecutionInput{initial.Input}, resources.inputs)
	pending, err = records.ListPendingWorkCreations(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
}

// The real preparation reports checkout outcomes before the Harness starts.
func completeCreationCheckout(t *testing.T, db *xorm.Engine, workID string) {
	t.Helper()
	receipts := infrastructure.NewInitialExecutionRequests(db, time.Now)
	planned, err := receipts.Get(t.Context(), workID)
	require.NoError(t, err)
	require.Len(t, planned.Checkouts, 1)
	require.NoError(t, command.NewRecordRepositoryCheckoutsHandler(receipts).Handle(t.Context(), command.RecordRepositoryCheckouts{
		WorkID: workID, Results: []command.RepositoryCheckoutResult{{RepositoryID: planned.Checkouts[0].RepoID, Prepared: true}},
	}))
}
