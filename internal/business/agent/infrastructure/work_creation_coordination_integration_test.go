//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

// creationCoordinator uses production creation, registration and preparation
// storage; only the execution side and catalogs are fixtures.
func creationCoordinator(db *xorm.Engine, capacity computertest.RoomCheck, registrar domain.AgentWorkRepository, options agentquery.ModelConfigurations, catalog commandtest.PluginCatalog) *command.CreateWorkHandler {
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	if registrar == nil {
		registrar = sessions
	}
	return command.NewCreateWorkHandler(time.Now, agents, registrar, auth, options, commandtest.StartCatalog(catalog, creationConnections{}), computertest.Checked(capacity), creationPreparation(db, options))
}

// A creation whose acknowledgement was lost after its commit is answered by
// its repeat through reconstructed stores: the same Work, which its request
// identity derives, and the first start derived from the Work's facts: its workspace prepared
// once its repository has an outcome, and its initial task input buffered
// once, before any later input.
func TestALostCreationAcknowledgementIsAnsweredByItsRepeat(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	lost := errors.New("acknowledgement lost")
	registrar := registrationLostAcknowledgement{AgentWorkRepository: f.sessions, err: lost}
	handler := creationCoordinator(f.db, startupAuthority{}, registrar, deploymentModels(), &creationCatalog{})
	_, err := handler.Handle(ctx, f.request)
	require.ErrorIs(t, err, lost)
	held, err := f.sessions.FindBySubject(ctx, f.request.AgentID, f.request.Subject)
	require.NoError(t, err)
	require.NotNil(t, held, "the creation committed")
	_, pending := held.PendingInitialTaskInput()
	require.True(t, pending, "the committed Work holds its initial task input")
	require.Empty(t, held.RepositoryCheckouts, "the first start's checkout has not ended")
	require.NoError(t, f.db.Close())

	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	handler = creationCoordinator(db, startupAuthority{}, nil, deploymentModels(), &creationCatalog{})
	id, err := handler.Handle(ctx, f.request)
	require.NoError(t, err)
	require.Equal(t, held.ID, id)
	require.Equal(t, id, requireStartRequested(t, infrastructure.NewAgentWorkRepository(db, time.Now), id).WorkID)

	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	session, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	preparations := infrastructure.NewWorkQueryRepository(db)
	preparation, found, err := preparations.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, preparation.RequestID, "the first start is named by the Work its creation request derived")
	require.False(t, preparation.InputPrepared, "the repository has no checkout outcome yet")
	require.False(t, preparation.InputBuffered)
	reportReady(t, session)
	require.True(t, session.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: id, Checkouts: []domain.RepositoryCheckout{{RepositoryID: "123", CheckedOut: true}}}))
	require.NoError(t, sessions.Save(ctx, session))
	preparation, _, err = preparations.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, preparation.InputPrepared)
	buffer := &commandtest.InputBuffer{}
	acceptance := command.NewInputAcceptance(sessions, buffer)
	require.NoError(t, acceptance.HandOffInitialTaskInput(ctx, id))
	require.NoError(t, acceptance.HandOffInitialTaskInput(ctx, id))
	require.Equal(t, []command.ExecutionInput{f.request.InitialExecution.Input}, buffer.Inputs)
	preparation, _, err = preparations.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, preparation.InputBuffered)
	require.Empty(t, preparation.Input, "a handed-off input is no longer held")
}

// A definitive refusal keeps nothing of its request: once capacity returns,
// the repeat is admitted and commits its Work, initial task input and first
// start together.
func TestADefinitiveCreationRefusalKeepsNothingOfItsRequest(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	capacity := &creationCapacity{err: domain.ErrCapacityUnavailable}
	handler := creationCoordinator(f.db, capacity, nil, deploymentModels(), &creationCatalog{})
	_, err := handler.Handle(ctx, f.request)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	actual, err := f.sessions.FindBySubject(ctx, f.request.AgentID, f.request.Subject)
	require.NoError(t, err)
	require.Nil(t, actual)

	capacity.err = nil
	id, err := handler.Handle(ctx, f.request)
	require.NoError(t, err)
	current, found, err := infrastructure.NewWorkQueryRepository(f.db).GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, current.Submitted, "the first start commits with the new Work")
	requireStartRequested(t, f.sessions, id)
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	pendingInput, pending := work.PendingInitialTaskInput()
	require.True(t, pending)
	require.Equal(t, domain.InitialTaskInput{ID: f.request.InitialExecution.Input.ID, Text: f.request.InitialExecution.Input.Text}, pendingInput)
}

// failedCommit loses the creation's transaction before it commits.
type failedCommit struct {
	domain.AgentWorkRepository
	err error
}

func (r *failedCommit) Save(ctx context.Context, work *domain.AgentWork) error {
	if r.err != nil && work.Version == 0 {
		return r.err
	}
	return r.AgentWorkRepository.Save(ctx, work)
}

// A commit failure after admission keeps the admitted Desk for the repeat,
// which asks for the same Desk and commits.
func TestACreationCommitFailureKeepsItsDesk(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	computer := &computertest.Admission{}
	registrar := &failedCommit{AgentWorkRepository: f.sessions, err: errors.New("connection lost during the commit")}
	create := f.creation(registrar, computer)
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

}
