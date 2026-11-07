//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

type creationCapacity struct{ err error }

func (c *creationCapacity) AdmitRoom(context.Context) error { return c.err }

// creationCoordinator uses production creation, registration, preparation and
// recovery storage; only execution delivery and catalogs are fixtures.
func creationCoordinator(db *xorm.Engine, capacity computertest.RoomCheck, starts *[]domain.StartExecutionRequest, registrar command.AgentWorkRepository, options agentquery.ModelConfigurations, catalog commandtest.PluginCatalog) (*command.CreateWorkHandler, *command.RecoverInitialExecutionHandler) {
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	if registrar == nil {
		registrar = sessions
	}
	execution := startupExecutionPort{onStart: func(r domain.StartExecutionRequest) error { *starts = append(*starts, r); return nil }}
	prepare := creationPreparation(db, execution, options)
	create := command.NewCreateWorkHandler(time.Now, agents, registrar, sessions, auth, options, commandtest.StartCatalog(catalog, creationConnections{}), computertest.Checked(capacity), execution, prepare)
	return create, command.NewRecoverInitialExecutionHandler(infrastructure.NewInitialExecutionRequests(db, time.Now), sessions, prepare, execution)
}

// A creation whose acknowledgement was lost after its commit is answered by
// its repeat through reconstructed stores: the same Work, one request record,
// and the first start continuing from the committed initial execution. The
// initial task input reaches the input buffer once, before any later input.
func TestALostCreationAcknowledgementIsAnsweredByItsRepeat(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	lost := errors.New("acknowledgement lost")
	registrar := registrationLostAcknowledgement{AgentWorkRepository: f.sessions, err: lost}
	var starts []domain.StartExecutionRequest
	handler, _ := creationCoordinator(f.db, startupAuthority{}, &starts, registrar, deploymentModels(), &creationCatalog{})
	_, err := handler.Handle(ctx, f.request)
	require.ErrorIs(t, err, lost)
	require.Empty(t, starts, "nothing starts on an unacknowledged commit")
	require.NoError(t, f.db.Close())

	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	handler, recovery := creationCoordinator(db, startupAuthority{}, &starts, nil, deploymentModels(), &creationCatalog{})
	id, err := handler.Handle(ctx, f.request)
	require.NoError(t, err)
	require.Len(t, starts, 1)
	require.Equal(t, id, starts[0].WorkID)
	require.Equal(t, 1, startRequestRows(t, db), "one request identity, one record")
	outcome, found, err := handler.Outcome(ctx, f.request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, outcome.WorkID)

	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	session, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	completeCreationCheckout(t, db, session.ID)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.Save(ctx, session))
	require.NoError(t, recovery.Handle(ctx, session.ID))
	buffer := &commandtest.InputBuffer{}
	acceptance := command.NewInputAcceptance(sessions, buffer)
	require.NoError(t, acceptance.HandOffInitialTaskInput(ctx, id))
	require.NoError(t, acceptance.HandOffInitialTaskInput(ctx, id))
	require.Equal(t, []command.ExecutionInput{f.request.InitialExecution.Input}, buffer.Inputs)
	pending, err := infrastructure.NewInitialExecutionRequests(db, time.Now).ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
}

// A definitive refusal settles its request identity: the repeat answers the
// cancellation even after capacity returns, while a new request identity is
// admitted and commits its Work, initial task input and first start together.
func TestADefinitiveCreationRefusalSettlesItsRequestIdentity(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	capacity := &creationCapacity{err: domain.ErrCapacityUnavailable}
	var starts []domain.StartExecutionRequest
	handler, _ := creationCoordinator(f.db, capacity, &starts, nil, deploymentModels(), &creationCatalog{})
	_, err := handler.Handle(ctx, f.request)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	require.ErrorIs(t, err, command.ErrWorkCreationCanceled)
	outcome, found, err := handler.Outcome(ctx, f.request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, outcome.Canceled, "a definitive refusal settles the identity")
	actual, err := f.sessions.FindWork(ctx, f.request.AgentID, f.request.Subject)
	require.NoError(t, err)
	require.Nil(t, actual)

	capacity.err = nil
	_, err = handler.Handle(ctx, f.request)
	require.ErrorIs(t, err, command.ErrWorkCreationCanceled, "repeating a refused identity answers its original outcome")
	require.Empty(t, starts, "restored capacity cannot silently retry a refused admission")

	retry := f.request
	initial := *retry.InitialExecution
	initial.RequestID, initial.Input.ID = uuid.NewString(), uuid.NewString()
	initial.Input.Text = "explicit retry after capacity rejection"
	retry.InitialExecution, retry.RequestID = &initial, initial.RequestID
	id, err := handler.Handle(ctx, retry)
	require.NoError(t, err)
	current, err := infrastructure.NewInitialExecutionRequests(f.db, time.Now).Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, initial.RequestID, current.RequestID, "the first start commits with the new Work")
	require.True(t, current.Submitted)
	require.False(t, current.Rejected)
	require.Len(t, starts, 1)
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	pendingInput, pending := work.PendingInitialTaskInput()
	require.True(t, pending)
	require.Equal(t, domain.InitialTaskInput{ID: initial.Input.ID, Text: initial.Input.Text}, pendingInput)
}

// failedCommit loses the creation's transaction before it commits.
type failedCommit struct {
	command.AgentWorkRepository
	err error
}

func (r *failedCommit) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	if r.err != nil {
		return r.err
	}
	return r.AgentWorkRepository.RegisterWork(ctx, work, registration)
}

// A commit failure after admission keeps the admitted Desk for the repeat,
// which asks for the same Desk and commits; a creation its caller cancelled
// first releases the Desk at once.
func TestACreationCommitFailureKeepsItsDesk(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	computer := &computertest.Admission{}
	registrar := &failedCommit{AgentWorkRepository: f.sessions, err: errors.New("connection lost during the commit")}
	create := f.creation(registrar, computer, executionPort{})
	_, err := create.Handle(ctx, f.request)
	require.ErrorIs(t, err, registrar.err)
	require.Equal(t, 1, computer.Open(), "an uncertain commit leaves its Desk to the repeat or the reconciliation")

	registrar.err = nil
	id, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
	require.Equal(t, 1, computer.Open(), "the repeat is seated on the same Desk")
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, work.State)

	cancelled := f.request
	initial := *cancelled.InitialExecution
	initial.RequestID = uuid.NewString()
	cancelled.InitialExecution, cancelled.RequestID = &initial, initial.RequestID
	cancelled.Subject = domain.WorkSubject{Kind: "case", ID: uuid.NewString()}
	paused := &pausedRegistration{AgentWorkRepository: f.sessions, reached: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := f.creation(paused, computer, executionPort{}).Handle(ctx, cancelled); result <- err }()
	select {
	case <-paused.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("creation did not reach registration")
	}
	require.Equal(t, 2, computer.Open())
	_, err = create.Cancel(ctx, cancelled.RequestID)
	close(paused.release)
	require.NoError(t, err)
	require.ErrorIs(t, <-result, command.ErrWorkCreationCanceled)
	require.Equal(t, 1, computer.Open(), "a cancelled creation releases its Desk")
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
