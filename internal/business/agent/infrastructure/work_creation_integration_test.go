//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/pkg/mysqltest"

	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

type workCreationFixture struct {
	db       *xorm.Engine
	dsn      string
	agents   *infrastructure.AgentDefinitionRepository
	sessions *infrastructure.AgentWorkRepository
	requests *infrastructure.WorkCreationRequests
	request  command.WorkCreationRequest
}

func newWorkCreationFixture(t *testing.T) workCreationFixture {
	return newWorkCreationFixtureForSubject(t, "owner")
}

func newWorkCreationFixtureForSubject(t *testing.T, subjectID string) workCreationFixture {
	f := newWorkDefinitionFixture(t, subjectID)
	retained, err := f.requests.RetainWorkCreation(context.Background(), f.request)
	require.NoError(t, err)
	f.request = retained
	return f
}

func newWorkDefinitionFixture(t *testing.T, subjectID string) workCreationFixture {
	return newWorkDefinitionFixtureWithConcurrency(t, subjectID, 1)
}

func newWorkDefinitionFixtureWithConcurrency(t *testing.T, subjectID string, limit int) workCreationFixture {
	t.Helper()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	requests := infrastructure.NewWorkCreationRequests(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{subjectID}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: subjectID}, HomeBinding: domain.HomeBinding{OwnerID: subjectID, Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(context.Background(), definition))
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-342", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "保留原始工作目标"}, Selections: []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "release"}}}
	request, err := command.NewWorkCreationRequest(command.CreateWork{StartingUserID: subjectID, RequestID: initial.RequestID, AgentID: definition.ID, SubjectID: subjectID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: uuid.NewString(), InitialExecution: &initial})
	require.NoError(t, err)
	return workCreationFixture{db: db, dsn: dsn, agents: agents, sessions: sessions, requests: requests, request: request}
}

// Both histories overlap the actual creation use case and cancellation through
// independent DB connections. The barrier controls which transaction commits
// first; registration, principals and initial input remain real MySQL writes.
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
			requests := infrastructure.NewWorkCreationRequests(other, time.Now)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			entered, resume := make(chan struct{}), make(chan struct{})
			var release sync.Once
			unpause := func() { release.Do(func() { close(resume) }) }
			defer unpause()
			lost := errors.New("registration acknowledgement lost")
			registrar := creationBarrierRegistrar{WorkRegistrar: f.sessions, afterCommit: registeredFirst, entered: entered, resume: resume, lost: lost}
			create := command.NewCreateWorkHandler(time.Now, f.agents, registrar, f.sessions, infrastructure.NewWorkAuthorizer(f.db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
			result := make(chan error, 1)
			go func() { _, err := create.Handle(ctx, f.request.Creation); result <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			canceled, err := requests.CancelWorkCreation(ctx, f.request.ID)
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
			actual, err := f.sessions.FindWork(ctx, f.request.Creation.AgentID, f.request.Creation.Subject)
			require.NoError(t, err)
			receipts := infrastructure.NewInitialExecutionRequests(other, time.Now)
			pending, err := receipts.ListPendingInitialExecutions(ctx, "", 100)
			require.NoError(t, err)
			if registeredFirst {
				require.False(t, canceled.Canceled)
				require.NotNil(t, actual)
				require.Equal(t, actual.ID, canceled.WorkID)
				require.Equal(t, domain.WorkStarting, actual.State)
				require.True(t, actual.HoldsProcess())
				require.Equal(t, []string{actual.ID}, pending)
				require.NoError(t, infrastructure.NewWorkAuthorizer(other).AuthorizeWork(ctx, "owner", actual.ID))
				require.NoError(t, requests.ConfirmWorkCreation(ctx, f.request.ID, actual.ID))
				require.NoError(t, requests.CompleteWorkCreation(ctx, f.request.ID))
			} else {
				require.True(t, canceled.Canceled)
				require.Empty(t, canceled.WorkID)
				require.Nil(t, actual)
				require.Empty(t, pending)
				require.ErrorIs(t, requests.ConfirmWorkCreation(ctx, f.request.ID, uuid.NewString()), command.ErrWorkCreationCanceled)
			}
			pending, err = requests.ListPendingWorkCreations(ctx, "", 100)
			require.NoError(t, err)
			require.Empty(t, pending)
		})
	}
}

type creationBarrierRegistrar struct {
	command.WorkRegistrar
	afterCommit bool
	entered     chan struct{}
	resume      chan struct{}
	lost        error
}

func (r creationBarrierRegistrar) RegisterWork(ctx context.Context, s *domain.AgentWork, registration command.WorkRegistration) error {
	if r.afterCommit {
		if err := r.WorkRegistrar.RegisterWork(ctx, s, registration); err != nil {
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
	return r.WorkRegistrar.RegisterWork(ctx, s, registration)
}

// Association confirmation is a prerequisite of initial preparation, including
// recovery after reconstructing every application store. It must guard both
// direct input and recovery before locks, execution submission or delivery.
func TestWorkCreationConfirmationGatesInitialRecovery(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	id, err := create.Handle(ctx, f.request.Creation)
	require.NoError(t, err)
	require.NoError(t, f.db.Close())
	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	auth := infrastructure.NewWorkAuthorizer(db)
	receipts := infrastructure.NewInitialExecutionRequests(db, time.Now)
	creations := infrastructure.NewWorkCreationRequests(db, time.Now)
	pending, err := creations.ListPendingWorkCreations(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{f.request.ID}, pending)
	retained, err := receipts.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, f.request.ID, retained.CreationRequestID)
	resources := &registrationResources{}
	var starts []domain.StartExecutionRequest
	requests := infrastructure.NewExecutionRequestsFactory(startupExecutionPort{onStart: func(r domain.StartExecutionRequest) error { starts = append(starts, r); return nil }})
	prepare := command.NewPrepareWorkExecutionHandler(receipts, sessions, auth, requests, creations, agentquery.ModelConfigurations{})
	recover := command.NewRecoverInitialExecutionHandler(receipts, sessions, prepare, resources, requests)
	require.ErrorIs(t, recover.Handle(ctx, id), command.ErrInitialExecutionPending)
	retry := retained.PrepareWorkExecution
	retry.CreationRequestID = "" // Transport retries cannot remove the stored prerequisite.
	retry.RequestID, retry.Input.ID = uuid.NewString(), uuid.NewString()
	_, err = prepare.Handle(ctx, retry)
	require.ErrorIs(t, err, command.ErrInitialExecutionPending)
	require.Empty(t, starts)
	require.Empty(t, resources.inputs)
	session, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, session.State)
	require.True(t, session.HoldsProcess())
	// This tests the Agent acknowledgement boundary. The cross-BC workflow must
	// obtain the caller's confirmed association before issuing this acknowledgement.
	require.NoError(t, creations.ConfirmWorkCreation(ctx, f.request.ID, id))
	require.ErrorIs(t, recover.Handle(ctx, id), command.ErrInitialExecutionPending)
	require.Len(t, starts, 1)
	session, err = sessions.Get(ctx, id)
	require.NoError(t, err)
	completeCreationCheckout(t, db, session.ID)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.Save(ctx, session))
	require.NoError(t, recover.Handle(ctx, id))
	require.NoError(t, recover.Handle(ctx, id))
	require.Equal(t, []command.ExecutionInput{f.request.Creation.InitialExecution.Input}, resources.inputs)
	pending, err = receipts.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
}

// A failure after writing the creation slot must roll back that slot together
// with Session registration. Otherwise cancellation would release nothing and
// recovery would permanently reference a Session that never existed.
func TestWorkCreationRegistrationRollbackAllowsCancellation(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	_, err := f.db.Exec(`CREATE TRIGGER reject_creation_input BEFORE INSERT ON agent_initial_execution_request FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='creation input write failed'`)
	require.NoError(t, err)
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	_, err = create.Handle(ctx, f.request.Creation)
	require.ErrorContains(t, err, "creation input write failed")
	retained, err := f.requests.GetWorkCreation(ctx, f.request.ID)
	require.NoError(t, err)
	require.Empty(t, retained.WorkID)
	require.False(t, retained.Canceled)
	session, err := f.sessions.FindWork(ctx, f.request.Creation.AgentID, f.request.Creation.Subject)
	require.NoError(t, err)
	require.Nil(t, session)
	_, err = f.db.Exec(`DROP TRIGGER reject_creation_input`)
	require.NoError(t, err)
	canceled, err := f.requests.CancelWorkCreation(ctx, f.request.ID)
	require.NoError(t, err)
	require.True(t, canceled.Canceled)
	_, err = create.Handle(ctx, f.request.Creation)
	require.ErrorIs(t, err, command.ErrWorkCreationCanceled)
	session, err = f.sessions.FindWork(ctx, f.request.Creation.AgentID, f.request.Creation.Subject)
	require.NoError(t, err)
	require.Nil(t, session)
}

func TestWorkCreationRetainsOriginalScopeAndInput(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	changed := f.request
	changed.Creation.SubjectID = "another-owner"
	initial := *changed.Creation.InitialExecution
	initial.SubjectID = changed.Creation.SubjectID
	changed.Creation.InitialExecution = &initial
	_, err := f.requests.RetainWorkCreation(ctx, changed)
	require.ErrorIs(t, err, command.ErrWorkCreationConflict)
	changed = f.request
	changed.Creation.AgentID = uuid.NewString()
	_, err = f.requests.RetainWorkCreation(ctx, changed)
	require.ErrorIs(t, err, command.ErrWorkCreationConflict)
	require.ErrorIs(t, f.requests.ConfirmWorkCreation(ctx, f.request.ID, uuid.NewString()), command.ErrWorkCreationConflict)
	retained, err := f.requests.GetWorkCreation(ctx, f.request.ID)
	require.NoError(t, err)
	require.Equal(t, f.request, retained)
	// Caller mutation cannot rewrite the durable retry payload.
	f.request.Creation.InitialExecution.Input.Text = "changed after return"
	f.request.Creation.Subject.ID = uuid.NewString()
	again, err := f.requests.GetWorkCreation(ctx, f.request.ID)
	require.NoError(t, err)
	require.Equal(t, retained, again)
	pending, err := f.requests.ListPendingWorkCreations(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{retained.ID}, pending)
}

// A real Session is insufficient: confirmation must correlate its represented
// human, definition, Subject and Project with the retained creation request.
func TestWorkCreationConfirmationRejectsForeignSession(t *testing.T) {
	for _, mismatch := range []string{"owner", "definition", "subject", "project"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newWorkCreationFixture(t)
			ctx := context.Background()
			creation := f.request.Creation
			creation.InitialExecution = nil
			switch mismatch {
			case "owner":
				// Another Owner of the same definition starts the Work.
				definition, err := f.agents.Get(ctx, creation.AgentID)
				require.NoError(t, err)
				_, err = definition.TransferOwners(append(definition.Owners, "another-owner"))
				require.NoError(t, err)
				require.NoError(t, f.agents.Save(ctx, definition))
				creation.SubjectID = "another-owner"
			case "definition":
				definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"another-owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "another-owner"}, HomeBinding: domain.HomeBinding{OwnerID: "another-owner", Namespace: "personal"}})
				require.NoError(t, err)
				require.NoError(t, f.agents.Save(ctx, definition))
				creation.AgentID = definition.ID
				creation.StartingUserID, creation.SubjectID = "another-owner", "another-owner"
			case "subject":
				creation.Subject = domain.WorkSubject{Kind: "case", ID: uuid.NewString()}
			case "project":
				creation.ProjectID = uuid.NewString()
			}
			create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
			id, err := create.Handle(ctx, creation)
			require.NoError(t, err)
			require.ErrorIs(t, f.requests.ConfirmWorkCreation(ctx, f.request.ID, id), command.ErrWorkCreationConflict)
			retained, err := f.requests.GetWorkCreation(ctx, f.request.ID)
			require.NoError(t, err)
			require.Empty(t, retained.WorkID)
			require.False(t, retained.BindingConfirmed)
			pending, err := f.requests.ListPendingWorkCreations(ctx, "", 100)
			require.NoError(t, err)
			require.Equal(t, []string{f.request.ID}, pending)
		})
	}
}

// Works without a Subject are created one per request identity: a repeated
// request returns its Work, another request creates another, and each keeps
// its read-only mounts. A Work's pending creation is its own request alone.
func TestWorksWithoutSubjectAreCreatedPerRequest(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	ctx := context.Background()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	startups := infrastructure.NewExecutionQueryRepository(f.db)
	mounts := []domain.ReadOnlyMount{{Name: "reference", Reference: "space-1"}}
	personal := func() command.WorkCreationRequest {
		initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), Input: command.ExecutionInput{ID: uuid.NewString(), Text: "my own work"}}
		request, err := command.NewWorkCreationRequest(command.CreateWork{StartingUserID: "owner", RequestID: initial.RequestID, AgentID: f.request.Creation.AgentID, SubjectID: "owner", ProjectID: "project-1", WorkspaceReference: uuid.NewString(), ReadOnlyMounts: mounts, InitialExecution: &initial})
		require.NoError(t, err)
		retained, err := f.requests.RetainWorkCreation(ctx, request)
		require.NoError(t, err)
		return retained
	}
	first, second := personal(), personal()
	firstID, err := create.Handle(ctx, first.Creation)
	require.NoError(t, err)
	repeated, err := create.Handle(ctx, first.Creation)
	require.NoError(t, err)
	require.Equal(t, firstID, repeated, "a repeated request returns its Work")
	secondID, err := create.Handle(ctx, second.Creation)
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID, "another request creates another Work")

	work, err := f.sessions.Get(ctx, firstID)
	require.NoError(t, err)
	require.False(t, work.Subject.Present())
	require.Equal(t, mounts, work.ReadOnlyMounts)

	require.NoError(t, f.requests.ConfirmWorkCreation(ctx, first.ID, firstID))
	require.NoError(t, f.requests.CompleteWorkCreation(ctx, first.ID))
	startup, err := startups.GetWorkStartup(ctx, firstID)
	require.NoError(t, err)
	require.False(t, startup.CreationPending, "another Work's pending creation is not this one's")
	startup, err = startups.GetWorkStartup(ctx, secondID)
	require.NoError(t, err)
	require.True(t, startup.CreationPending)
}

// competingRegistration lets another creation of the same request register
// first, as a concurrent repeat would.
type competingRegistration struct {
	command.WorkRegistrar
	compete func()
}

func (r *competingRegistration) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	if r.compete != nil {
		compete := r.compete
		r.compete = nil
		compete()
	}
	return r.WorkRegistrar.RegisterWork(ctx, work, registration)
}

// A concurrent repeat of a request without a Subject that loses registration
// returns the Work the winner registered.
func TestAConcurrentRepeatWithoutSubjectReturnsTheWinnersWork(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	ctx := context.Background()
	handler := func(registrar command.WorkRegistrar) *command.CreateWorkHandler {
		return command.NewCreateWorkHandler(time.Now, f.agents, registrar, f.sessions, infrastructure.NewWorkAuthorizer(f.db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	}
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), Input: command.ExecutionInput{ID: uuid.NewString(), Text: "my own work"}}
	request, err := command.NewWorkCreationRequest(command.CreateWork{StartingUserID: "owner", RequestID: initial.RequestID, AgentID: f.request.Creation.AgentID, SubjectID: "owner", WorkID: uuid.NewString(), WorkspaceReference: uuid.NewString(), InitialExecution: &initial})
	require.NoError(t, err)
	retained, err := f.requests.RetainWorkCreation(ctx, request)
	require.NoError(t, err)

	var winner string
	registrar := &competingRegistration{WorkRegistrar: f.sessions}
	registrar.compete = func() {
		winner, err = handler(f.sessions).Handle(ctx, retained.Creation)
		require.NoError(t, err)
	}
	loser, err := handler(registrar).Handle(ctx, retained.Creation)
	require.NoError(t, err)
	require.Equal(t, winner, loser)
	require.Equal(t, request.Creation.WorkID, loser, "the Work keeps the identity its caller allocated")
}
