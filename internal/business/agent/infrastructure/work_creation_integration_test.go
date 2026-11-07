//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"sync"
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
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{subjectID}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: subjectID}, HomeBinding: domain.HomeBinding{OwnerID: subjectID, Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(context.Background(), definition))
	initial := command.PrepareWorkExecution{SubjectID: subjectID, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-342", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "保留原始工作目标"}, Selections: []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "release"}}}
	request := command.CreateWork{StartingUserID: subjectID, RequestID: initial.RequestID, AgentID: definition.ID, SubjectID: subjectID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: uuid.NewString(), InitialExecution: &initial}
	return workCreationFixture{db: db, dsn: dsn, agents: agents, sessions: sessions, request: request}
}

func deploymentModels() agentquery.ModelConfigurations {
	return agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
}

// creationPreparation is the real first-start preparation a creation carrying
// an initial execution continues with.
func creationPreparation(db *xorm.Engine, models agentquery.ModelConfigurations) *command.PrepareWorkExecutionHandler {
	return command.NewPrepareWorkExecutionHandler(infrastructure.NewAgentWorkRepository(db, time.Now), access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), models)
}

// creation is the production creation use case over this fixture's database;
// registrar may wrap the Work repository to inject a failure.
func (f workCreationFixture) creation(registrar command.AgentWorkRepository, computer command.ComputerPort) *command.CreateWorkHandler {
	if registrar == nil {
		registrar = f.sessions
	}
	return command.NewCreateWorkHandler(time.Now, f.agents, registrar, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), deploymentModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computer, creationPreparation(f.db, deploymentModels()))
}

func startRequestRows(t *testing.T, db *xorm.Engine) int {
	t.Helper()
	var counts []int
	require.NoError(t, db.SQL("SELECT COUNT(*) FROM agent_start_request").Find(&counts))
	return counts[0]
}

// Both histories overlap the creation use case and its cancellation through
// independent DB connections. The barrier controls which transaction commits
// first; the request record decides the outcome for both.
func TestWorkCreationCancellationAndRegistration(t *testing.T) {
	for _, registeredFirst := range []bool{false, true} {
		name := "cancellation commits first"
		if registeredFirst {
			name = "registration commits first and acknowledgement is lost"
		}
		t.Run(name, func(t *testing.T) {
			f := newWorkCreationFixture(t)
			other, err := xorm.NewEngine("mysql", f.dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = other.Close() })
			canceller := workCreationFixture{db: other, dsn: f.dsn, agents: infrastructure.NewAgentDefinitionRepository(other, time.Now), sessions: infrastructure.NewAgentWorkRepository(other, time.Now)}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			entered, resume := make(chan struct{}), make(chan struct{})
			var release sync.Once
			unpause := func() { release.Do(func() { close(resume) }) }
			defer unpause()
			lost := errors.New("registration acknowledgement lost")
			registrar := creationBarrierRegistrar{AgentWorkRepository: f.sessions, afterCommit: registeredFirst, entered: entered, resume: resume, lost: lost}
			create := f.creation(registrar, computertest.Checked(startupAuthority{}))
			result := make(chan error, 1)
			go func() { _, err := create.Handle(ctx, f.request); result <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			workID, err := canceller.creation(nil, computertest.Checked(startupAuthority{})).Cancel(ctx, f.request.RequestID)
			require.NoError(t, err)
			unpause()
			select {
			case err := <-result:
				if registeredFirst {
					require.ErrorIs(t, err, lost)
				} else {
					require.ErrorIs(t, err, command.ErrWorkCreationCanceled)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			actual, err := f.sessions.FindBySubject(ctx, f.request.AgentID, f.request.Subject)
			require.NoError(t, err)
			if registeredFirst {
				require.NotNil(t, actual)
				require.Equal(t, actual.ID, workID, "cancelling a committed creation returns its Work")
				require.Equal(t, domain.WorkStarting, actual.State)
				require.True(t, actual.HoldsProcess())
				input, held := actual.PendingInitialTaskInput()
				require.True(t, held, "the committed Work holds its initial task input")
				require.Equal(t, f.request.InitialExecution.Input.ID, input.ID)
				require.NoError(t, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(other)).AuthorizeWork(ctx, "owner", actual.ID))
			} else {
				require.Empty(t, workID)
				require.Nil(t, actual, "a cancelled creation creates nothing")
			}
			require.Equal(t, 1, startRequestRows(t, f.db))
		})
	}
}

type creationBarrierRegistrar struct {
	command.AgentWorkRepository
	afterCommit bool
	entered     chan struct{}
	resume      chan struct{}
	lost        error
}

func (r creationBarrierRegistrar) RegisterWork(ctx context.Context, s *domain.AgentWork, registration command.WorkRegistration) error {
	if r.afterCommit {
		if err := r.AgentWorkRepository.RegisterWork(ctx, s, registration); err != nil {
			return err
		}
	}
	close(r.entered)
	select {
	case <-r.resume:
	case <-ctx.Done():
		return ctx.Err()
	}
	if r.afterCommit {
		return r.lost
	}
	return r.AgentWorkRepository.RegisterWork(ctx, s, registration)
}

// A failure after writing the request record rolls it back together with the
// Work, so the identity stays unanswered: its caller may still cancel it, and
// the cancellation then holds against a delayed repeat.
func TestWorkCreationRegistrationRollbackAllowsCancellation(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	_, err := f.db.Exec(`CREATE TRIGGER reject_creation_request AFTER INSERT ON agent_start_request FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='creation request write failed'`)
	require.NoError(t, err)
	create := f.creation(nil, computertest.Checked(startupAuthority{}))
	_, err = create.Handle(ctx, f.request)
	require.ErrorContains(t, err, "creation request write failed")
	_, found, err := create.Outcome(ctx, f.request.RequestID)
	require.NoError(t, err)
	require.False(t, found, "a rolled-back creation records nothing")
	session, err := f.sessions.FindBySubject(ctx, f.request.AgentID, f.request.Subject)
	require.NoError(t, err)
	require.Nil(t, session)
	_, err = f.db.Exec(`DROP TRIGGER reject_creation_request`)
	require.NoError(t, err)
	workID, err := create.Cancel(ctx, f.request.RequestID)
	require.NoError(t, err)
	require.Empty(t, workID)
	_, err = create.Handle(ctx, f.request)
	require.ErrorIs(t, err, command.ErrWorkCreationCanceled)
	session, err = f.sessions.FindBySubject(ctx, f.request.AgentID, f.request.Subject)
	require.NoError(t, err)
	require.Nil(t, session)
}

// The first creation decides its request identity: a repeat with other
// content is a conflict, and the caller mutating its request afterwards does
// not rewrite what was recorded.
func TestWorkCreationRetainsOriginalContent(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := f.creation(nil, computertest.Checked(startupAuthority{}))
	id, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
	for name, mutate := range map[string]func(*command.CreateWork){
		"subject":    func(r *command.CreateWork) { r.Subject.ID = uuid.NewString() },
		"definition": func(r *command.CreateWork) { r.AgentID = uuid.NewString() },
		"input":      func(r *command.CreateWork) { r.InitialExecution.Input.Text = "changed after return" },
	} {
		changed := f.request
		initial := *f.request.InitialExecution
		changed.InitialExecution = &initial
		mutate(&changed)
		_, err := create.Handle(ctx, changed)
		require.ErrorIs(t, err, command.ErrWorkCreationConflict, name)
	}
	repeated, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
	require.Equal(t, id, repeated)
	outcome, found, err := create.Outcome(ctx, f.request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, outcome.WorkID)
	require.False(t, outcome.Canceled)
	require.Equal(t, 1, startRequestRows(t, f.db))
}

// Works without a Subject are created one per request identity: a repeated
// request returns its Work, another request creates another, and each keeps
// its read-only mounts. A Work's creation is pending only until its own
// initial task input is handed off.
func TestWorksWithoutSubjectAreCreatedPerRequest(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := f.creation(nil, computertest.Checked(startupAuthority{}))
	startups := infrastructure.NewExecutionQueryRepository(f.db)
	mounts := []domain.ReadOnlyMount{{Name: "reference", Reference: "space-1"}}
	personal := func() command.CreateWork {
		initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), Input: command.ExecutionInput{ID: uuid.NewString(), Text: "my own work"}}
		return command.CreateWork{StartingUserID: "owner", RequestID: initial.RequestID, AgentID: f.request.AgentID, SubjectID: "owner", ProjectID: "project-1", WorkspaceReference: uuid.NewString(), ReadOnlyMounts: mounts, InitialExecution: &initial}
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
	require.Equal(t, mounts, work.ReadOnlyMounts)

	for _, id := range []string{firstID, secondID} {
		startup, err := startups.GetWorkStartup(ctx, id)
		require.NoError(t, err)
		require.True(t, startup.CreationPending, "a new Work holds its initial task input")
	}
	buffer := &commandtest.InputBuffer{}
	require.NoError(t, command.NewInputAcceptance(f.sessions, buffer).HandOffInitialTaskInput(ctx, firstID))
	require.Equal(t, []command.ExecutionInput{first.InitialExecution.Input}, buffer.Inputs)
	startup, err := startups.GetWorkStartup(ctx, firstID)
	require.NoError(t, err)
	require.False(t, startup.CreationPending, "a handed-off initial task input leaves the Work")
	startup, err = startups.GetWorkStartup(ctx, secondID)
	require.NoError(t, err)
	require.True(t, startup.CreationPending, "another Work's hand-off is not this one's")
}

// competingRegistration lets another creation of the same request register
// first, as a concurrent repeat would.
type competingRegistration struct {
	command.AgentWorkRepository
	compete func()
}

func (r *competingRegistration) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	if r.compete != nil {
		compete := r.compete
		r.compete = nil
		compete()
	}
	return r.AgentWorkRepository.RegisterWork(ctx, work, registration)
}

// A concurrent repeat of a request without a Subject that loses registration
// returns the Work the winner registered.
func TestAConcurrentRepeatWithoutSubjectReturnsTheWinnersWork(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), Input: command.ExecutionInput{ID: uuid.NewString(), Text: "my own work"}}
	request := command.CreateWork{StartingUserID: "owner", RequestID: initial.RequestID, AgentID: f.request.AgentID, SubjectID: "owner", WorkID: uuid.NewString(), WorkspaceReference: uuid.NewString(), InitialExecution: &initial}

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
	require.Equal(t, 1, startRequestRows(t, f.db))
}

// admittedStart reads a start request identity as GetStartAdmission does:
// found only when a Work admitted it.
func admittedStart(t *testing.T, works command.AgentWorkRepository, requestID string) (agentquery.AdmittedStart, bool) {
	t.Helper()
	record, found, err := works.FindStartRequest(t.Context(), requestID)
	require.NoError(t, err)
	if !found || !record.Admitted() {
		return agentquery.AdmittedStart{}, false
	}
	return agentquery.AdmittedStart{WorkID: record.WorkID, Kind: record.Kind}, true
}

// committedCreation creates the fixture's Work and loses the answer right
// after the commit, so the Work and its retained initial execution are left
// as the creation committed them, before the first start's preparation.
func (f workCreationFixture) committedCreation(t *testing.T) string {
	t.Helper()
	lost := errors.New("creation answer lost after the commit")
	_, err := f.creation(registrationLostAcknowledgement{AgentWorkRepository: f.sessions, err: lost}, computertest.Checked(startupAuthority{})).Handle(t.Context(), f.request)
	require.ErrorIs(t, err, lost)
	outcome, found, err := f.creation(nil, computertest.Checked(startupAuthority{})).Outcome(t.Context(), f.request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	return outcome.WorkID
}
