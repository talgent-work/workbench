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

// Registration durably retains the Work and its original input together.
// Lose its acknowledgement, reconstruct all stores, and continue the first
// start from the Work's own facts: an unreachable repository degrades once,
// and a later report never replays it.
func TestInitialSessionRegistrationSurvivesLostAcknowledgement(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, definition))
	input := command.PrepareWorkExecution{SubjectID: "owner", RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-338", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Original input\n保留初始请求"}, Selections: []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "release"}}}
	creation := command.CreateWork{AgentID: definition.ID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: "project"}
	lost := errors.New("registration committed; acknowledgement lost")
	registrar := registrationLostAcknowledgement{AgentWorkRepository: sessions, err: lost}
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
	create := command.NewCreateWorkHandler(time.Now, agents, registrar, auth, models, commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), command.NewPrepareWorkExecutionHandler(sessions, auth, agentquery.ModelConfigurations{}))
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
	reader := infrastructure.NewWorkQueryRepository(db)
	submitted, err := reader.ListSubmittedPreparations(ctx, "", 100)
	require.NoError(t, err)
	require.Len(t, submitted, 1, "a committed Work already carries a discoverable first start")
	session, err := sessions.Get(ctx, submitted[0].WorkID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, session.State)
	require.True(t, session.HoldsProcess())
	require.NoError(t, auth.AuthorizeWork(ctx, "owner", session.ID))
	require.Equal(t, input.RequestID, submitted[0].RequestID)
	require.Equal(t, input.Input.ID, submitted[0].InputID)
	require.Equal(t, input.Input.Text, submitted[0].Input)
	require.Equal(t, input.WorkBranch, submitted[0].WorkBranch)
	require.False(t, submitted[0].InputPrepared)
	require.True(t, submitted[0].Submitted, "first admission is committed with the original input")
	require.Equal(t, []string{"123"}, session.LaunchSpecification().RemainingRepositories)
	requireStartRequested(t, sessions, session.ID)
	observed, err := infrastructure.NewServiceAccessResults(db).ReadRepositoryPreparation(ctx, session.ID)
	require.NoError(t, err)
	require.Empty(t, observed, "no repository has a checkout outcome yet")

	buffer := &commandtest.InputBuffer{}
	create = command.NewCreateWorkHandler(time.Now, agents, sessions, auth, models, commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), command.NewPrepareWorkExecutionHandler(sessions, auth, agentquery.ModelConfigurations{}))
	// The start's execution reports its checkout before the Harness starts;
	// an unreachable repository degrades.
	ref := session.ID
	recordWorkCheckouts(t, sessions, ref, domain.RepositoryCheckout{RepositoryID: "123", Reason: domain.CheckoutUnavailable})
	session, err = sessions.Get(ctx, ref)
	require.NoError(t, err)
	reportReady(t, session)
	require.NoError(t, sessions.Save(ctx, session))
	require.NoError(t, command.NewInputAcceptance(sessions, buffer).HandOffInitialTaskInput(ctx, ref))
	require.Equal(t, []command.ExecutionInput{input.Input}, buffer.Inputs)
	prepared, found, err := reader.GetWorkPreparation(ctx, ref)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, prepared.InputPrepared)
	require.True(t, prepared.InputBuffered)
	require.Len(t, prepared.Checkouts, 1)
	observed, err = infrastructure.NewServiceAccessResults(db).ReadRepositoryPreparation(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, []agentquery.RepositoryPreparation{{RepositoryID: "123", TargetPath: prepared.Checkouts[0].TargetPath, Reason: string(domain.CheckoutUnavailable)}}, observed)
	continued, err := sessions.Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, continued.State)
	require.False(t, continued.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: ref, Checkouts: []domain.RepositoryCheckout{{RepositoryID: "123", CheckedOut: true}}}), "a later report never replays the degraded checkout")
	unchanged, err := infrastructure.NewServiceAccessResults(db).ReadRepositoryPreparation(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, observed, unchanged)
	// A repeat with new transport IDs locates the original Work and input;
	// an incompatible repeat must not alter it or create another Work.
	repeat := input
	repeat.RequestID, repeat.Input.ID = uuid.NewString(), uuid.NewString()
	repeated, err := prepareNew(ctx, creation, repeat)
	require.NoError(t, err)
	require.Equal(t, ref, repeated)
	// Repeating that request identity with other content is a conflict; a new
	// request with other repository choices cannot replace the original ones.
	repeat.Input.Text = "different task"
	_, err = prepareNew(ctx, creation, repeat)
	require.ErrorIs(t, err, command.ErrWorkCreationConflict)
	repeat.RequestID, repeat.Input.ID = uuid.NewString(), uuid.NewString()
	repeat.Selections = []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "main"}}
	_, err = prepareNew(ctx, creation, repeat)
	require.ErrorIs(t, err, command.ErrInitialExecutionConflict)
	requireStartRequested(t, sessions, ref)
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
