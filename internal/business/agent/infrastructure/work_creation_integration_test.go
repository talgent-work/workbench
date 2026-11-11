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
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

type workCreationFixture struct {
	db       *xorm.Engine
	dsn      string
	agents   *infrastructure.AgentDefinitionRepository
	sessions *infrastructure.AgentWorkRepository
	// request creates a Work for a Subject with an initial task input.
	request command.CreateWork
}

func newWorkCreationFixture(t *testing.T) workCreationFixture {
	return newWorkDefinitionFixture(t, "owner")
}

func newWorkDefinitionFixture(t *testing.T, subjectID string) workCreationFixture {
	t.Helper()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{subjectID}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: subjectID}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(context.Background(), definition))
	initial := command.PrepareWorkExecution{SubjectID: subjectID, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-342", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "保留原始工作目标"}, Selections: []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "release"}}}
	request := command.CreateWork{StartingUserID: subjectID, RequestID: initial.RequestID, AgentID: definition.ID, SubjectID: subjectID, Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: uuid.NewString(), InitialExecution: &initial}
	return workCreationFixture{db: db, dsn: dsn, agents: agents, sessions: sessions, request: request}
}

func deploymentModels() agentquery.ModelConfigurations {
	return agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
}

// creationPreparation is the real first-start preparation a creation carrying
// an initial execution continues with.
func creationPreparation(db *xorm.Engine, models agentquery.ModelConfigurations) *command.PrepareWorkExecutionHandler {
	return command.NewPrepareWorkExecutionHandler(infrastructure.NewAgentWorkRepository(db, time.Now), access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), models)
}

// creation is the production creation use case over this fixture's database;
// registrar may wrap the Work repository to inject a failure.
func (f workCreationFixture) creation(registrar domain.AgentWorkRepository, computer command.ComputerPort) *command.CreateWorkHandler {
	if registrar == nil {
		registrar = f.sessions
	}
	return command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, f.agents, registrar, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), deploymentModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computer, creationPreparation(f.db, deploymentModels()))
}

// A repeated creation returns the Work its request identity derives,
// whatever else the repeat carries; an identity whose Work belongs to another
// definition names another operation.
func TestARepeatedCreationReturnsTheWorkItsIdentityDerives(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := f.creation(nil, computertest.Checked(startupAuthority{}))
	id, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
	for name, mutate := range map[string]func(*command.CreateWork){
		"subject": func(r *command.CreateWork) { r.Subject.ID = uuid.NewString() },
		"input":   func(r *command.CreateWork) { r.InitialExecution.Input.Text = "changed after return" },
	} {
		changed := f.request
		initial := *f.request.InitialExecution
		changed.InitialExecution = &initial
		mutate(&changed)
		repeated, err := create.Handle(ctx, changed)
		require.NoError(t, err, name)
		require.Equal(t, id, repeated, name)
	}
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, f.request.Subject, work.Subject, "a repeat changes nothing of its Work")
	pending, held := work.PendingInitialTaskInput()
	require.True(t, held)
	require.Equal(t, f.request.InitialExecution.Input.Text, pending.Text)

	other, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Other Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}})
	require.NoError(t, err)
	require.NoError(t, f.agents.Save(ctx, other))
	foreign := f.request
	foreign.AgentID = other.ID
	_, err = create.Handle(ctx, foreign)
	require.ErrorIs(t, err, command.ErrStartRequestConflict)
}

// Works without a Subject are created one per request identity: a repeated
// request returns its Work, another request creates another, and each
// locates its own inputs. A Work's creation is pending only until its own
// initial task input is handed off.
func TestWorksWithoutSubjectAreCreatedPerRequest(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := f.creation(nil, computertest.Checked(startupAuthority{}))
	personal := func() command.CreateWork {
		initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), Input: command.ExecutionInput{ID: uuid.NewString(), Text: "my own work"}}
		return command.CreateWork{StartingUserID: "owner", RequestID: initial.RequestID, AgentID: f.request.AgentID, SubjectID: "owner", ProjectID: "project-1", InitialExecution: &initial}
	}
	first, second := personal(), personal()
	firstID, err := create.Handle(ctx, first)
	require.NoError(t, err)
	repeated, err := create.Handle(ctx, first)
	require.NoError(t, err)
	require.Equal(t, firstID, repeated, "a repeated request returns its Work")
	secondID, err := create.Handle(ctx, second)
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID, "another request creates another Work")

	work, err := f.sessions.Get(ctx, firstID)
	require.NoError(t, err)
	require.False(t, work.Subject.Present())
	require.Equal(t, []domain.OwnerFact{{Condition: domain.ConditionWork, Value: firstID}}, work.LocatedLayout.Inputs.Directory.Owner, "a Work without a Subject reads its own inputs")

	holds := func(id string) bool {
		work, err := f.sessions.Get(ctx, id)
		require.NoError(t, err)
		_, held := work.PendingInitialTaskInput()
		return held
	}
	require.True(t, holds(firstID), "a new Work holds its initial task input")
	require.True(t, holds(secondID))
	buffer := &commandtest.InputBuffer{}
	require.NoError(t, command.NewInputAcceptance(f.sessions, buffer).HandOffInitialTaskInput(ctx, firstID))
	require.Equal(t, []command.ExecutionInput{first.InitialExecution.Input}, buffer.Inputs)
	require.False(t, holds(firstID), "a handed-off initial task input leaves the Work")
	require.True(t, holds(secondID), "another Work's hand-off is not this one's")
}

// competingRegistration lets another creation of the same request register
// first, as a concurrent repeat would.
type competingRegistration struct {
	domain.AgentWorkRepository
	compete func()
}

func (r *competingRegistration) Save(ctx context.Context, work *domain.AgentWork) error {
	if r.compete != nil && work.Version == 0 {
		compete := r.compete
		r.compete = nil
		compete()
	}
	return r.AgentWorkRepository.Save(ctx, work)
}

// A concurrent repeat of a request without a Subject that loses registration
// returns the Work the winner registered.
func TestAConcurrentRepeatWithoutSubjectReturnsTheWinnersWork(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), Input: command.ExecutionInput{ID: uuid.NewString(), Text: "my own work"}}
	request := command.CreateWork{StartingUserID: "owner", RequestID: initial.RequestID, AgentID: f.request.AgentID, SubjectID: "owner", WorkID: uuid.NewString(), InitialExecution: &initial}

	var winner string
	var err error
	registrar := &competingRegistration{AgentWorkRepository: f.sessions}
	registrar.compete = func() {
		winner, err = f.creation(nil, computertest.Checked(startupAuthority{})).Handle(ctx, request)
		require.NoError(t, err)
	}
	loser, err := f.creation(registrar, computertest.Checked(startupAuthority{})).Handle(ctx, request)
	require.NoError(t, err)
	require.Equal(t, winner, loser)
	require.Equal(t, request.WorkID, loser, "the Work keeps the identity its caller allocated")
}

// committedCreation creates the fixture's Work and loses the answer right
// after the commit, so the Work and its retained initial execution are left
// as the creation committed them, before the first start's preparation.
func (f workCreationFixture) committedCreation(t *testing.T) string {
	t.Helper()
	lost := errors.New("creation answer lost after the commit")
	_, err := f.creation(registrationLostAcknowledgement{AgentWorkRepository: f.sessions, err: lost}, computertest.Checked(startupAuthority{})).Handle(t.Context(), f.request)
	require.ErrorIs(t, err, lost)
	work, err := f.sessions.FindBySubject(t.Context(), f.request.AgentID, f.request.Subject)
	require.NoError(t, err)
	require.NotNil(t, work, "the creation committed")
	return work.ID
}
