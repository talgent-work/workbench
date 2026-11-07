//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

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

// Registration durably retains the Session and original input together.
// Lose its acknowledgement before any input locks or execution submission, then
// reconstruct all stores and recover only from the database scan.
func TestInitialSessionRegistrationSurvivesLostAcknowledgement(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	receipts := infrastructure.NewInitialExecutionRequests(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, definition))
	input := command.PrepareWorkExecution{SubjectID: "owner", RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-338", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Original input\n保留初始请求"}, Selections: []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "release"}}}
	creation := command.CreateWork{AgentID: definition.ID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: "project"}
	lost := errors.New("registration committed; acknowledgement lost")
	registrar := registrationLostAcknowledgement{AgentWorkRepository: sessions, err: lost}
	prepare := command.NewPrepareWorkExecutionHandler(receipts, sessions, auth, agentquery.ModelConfigurations{})
	create := command.NewCreateWorkHandler(time.Now, agents, registrar, auth, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), prepare)
	prepareNew := func(ctx context.Context, creation command.CreateWork, input command.PrepareWorkExecution) (string, error) {
		creation.SubjectID, creation.RequestID, creation.InitialExecution = input.SubjectID, input.RequestID, &input
		return create.Handle(ctx, creation)
	}
	_, err = prepareNew(ctx, creation, input)
	require.ErrorIs(t, err, lost)
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	agents = infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
	auth = access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	receipts = infrastructure.NewInitialExecutionRequests(db, time.Now)
	pending, err := receipts.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Len(t, pending, 1, "a committed Session must already carry a discoverable original request")
	session, err := sessions.Get(ctx, pending[0])
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, session.State)
	require.True(t, session.HoldsProcess())
	require.NoError(t, auth.AuthorizeWork(ctx, "owner", session.ID))
	retained, err := receipts.Get(ctx, session.ID)
	require.NoError(t, err)
	input.WorkID, input.WorkspaceReference = session.ID, creation.WorkspaceReference
	require.Equal(t, input, retained.PrepareWorkExecution)
	require.False(t, retained.Prepared)
	require.True(t, retained.Submitted, "first admission is committed with the original input")
	buffer := &commandtest.InputBuffer{}
	prepare = command.NewPrepareWorkExecutionHandler(receipts, sessions, auth, agentquery.ModelConfigurations{})
	create = command.NewCreateWorkHandler(time.Now, agents, sessions, auth, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), prepare)
	recover := command.NewRecoverInitialExecutionHandler(receipts, sessions, prepare)
	require.ErrorIs(t, recover.Handle(ctx, session.ID), command.ErrInitialExecutionPending)
	requireStartIntended(t, sessions, session.ID)
	// The start's preparation process reports its planned checkout before the
	// Harness starts; an unreachable repository degrades.
	planned, err := receipts.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, planned.Checkouts, 1)
	checkouts := command.NewRecordRepositoryCheckoutsHandler(receipts)
	require.NoError(t, checkouts.Handle(ctx, command.RecordRepositoryCheckouts{WorkID: session.ID, Results: []command.RepositoryCheckoutResult{{RepositoryID: planned.Checkouts[0].RepoID, Reason: command.CheckoutUnavailable}}}))
	session, err = sessions.Get(ctx, session.ID)
	require.NoError(t, err)
	ref := session.ID
	reportReady(t, session)
	require.NoError(t, sessions.Save(ctx, session))
	require.NoError(t, recover.Handle(ctx, session.ID))
	require.NoError(t, command.NewInputAcceptance(sessions, buffer).HandOffInitialTaskInput(ctx, session.ID))
	require.Equal(t, []command.ExecutionInput{input.Input}, buffer.Inputs)
	prepared, err := receipts.Get(ctx, session.ID)
	require.NoError(t, err)
	require.True(t, prepared.InputPrepared)
	require.Len(t, prepared.CheckoutResults, 1)
	require.Equal(t, command.RepositoryCheckoutResult{RepositoryID: prepared.Checkouts[0].RepoID, TargetPath: prepared.Checkouts[0].TargetPath, Reason: "checkout_unavailable"}, prepared.CheckoutResults[0])
	require.False(t, prepared.Rejected)
	observed, err := infrastructure.NewServiceAccessResults(db).ReadRepositoryPreparation(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, observed, 1)
	require.Equal(t, prepared.Checkouts[0].RepoID, observed[0].RepositoryID)
	require.False(t, observed[0].Prepared)
	require.Equal(t, "checkout_unavailable", observed[0].Reason)
	continued, err := sessions.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, continued.State)
	require.NoError(t, recover.Handle(ctx, session.ID))
	require.NoError(t, checkouts.Handle(ctx, command.RecordRepositoryCheckouts{WorkID: session.ID, Results: []command.RepositoryCheckoutResult{{RepositoryID: planned.Checkouts[0].RepoID, Prepared: true}}}))
	unchanged, err := receipts.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, prepared.CheckoutResults, unchanged.CheckoutResults, "a later report never replays the degraded checkout")
	pending, err = receipts.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
	// A repeat with new transport IDs locates the original binding and input;
	// an incompatible repeat must not alter that obligation or create another Session.
	repeat := input
	repeat.WorkID, repeat.WorkspaceReference = "", ""
	repeat.RequestID, repeat.Input.ID = uuid.NewString(), uuid.NewString()
	repeated, err := prepareNew(ctx, creation, repeat)
	require.NoError(t, err)
	require.Equal(t, ref, repeated)
	// Repeating that request identity with other content is a conflict; a new
	// request with another task cannot replace the original one.
	repeat.Input.Text = "different task"
	_, err = prepareNew(ctx, creation, repeat)
	require.ErrorIs(t, err, command.ErrWorkCreationConflict)
	repeat.RequestID, repeat.Input.ID = uuid.NewString(), uuid.NewString()
	_, err = prepareNew(ctx, creation, repeat)
	require.ErrorIs(t, err, command.ErrInitialExecutionConflict)
	requireStartIntended(t, sessions, ref)
	require.Len(t, buffer.Inputs, 1)
}

type registrationLostAcknowledgement struct {
	command.AgentWorkRepository
	err error
}

func (r registrationLostAcknowledgement) RegisterWork(ctx context.Context, s *domain.AgentWork, registration command.WorkRegistration) error {
	if err := r.AgentWorkRepository.RegisterWork(ctx, s, registration); err != nil {
		return err
	}
	return r.err
}
