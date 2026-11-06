//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/talgent-work/workbench/pkg/mysqltest"

	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// Both requests reach the real admission transaction before either commits.
// The barrier exercises request-identity ownership across independent Works.
type admissionSaveBarrier struct {
	*infrastructure.AgentWorkRepository
	arrived chan struct{}
	proceed chan struct{}
}

func (b admissionSaveBarrier) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	select {
	case b.arrived <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-b.proceed:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.AgentWorkRepository.RegisterWork(ctx, work, registration)
}

func TestConcurrentStartRequestOwnershipPreservesWinnerAndRollsBackLoser(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	auth := infrastructure.NewWorkAuthorizer(db)
	newCreation := func(requestID string) command.CreateWork {
		agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.Must(uuid.NewV7()).String()}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Run"}})
		require.NoError(t, err)
		require.NoError(t, agents.Save(ctx, agent))
		return command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: requestID, SubjectID: "owner", AgentID: agent.ID, WorkspaceReference: "workspace"}
	}
	requestID := uuid.Must(uuid.NewV7()).String()
	creations := []command.CreateWork{newCreation(requestID), newCreation(requestID)}
	var starts atomic.Int32
	factory := infrastructure.NewExecutionRequestsFactory(startupExecutionPort{onStart: func(domain.StartExecutionRequest) error { starts.Add(1); return nil }})
	barrier := admissionSaveBarrier{AgentWorkRepository: sessions, arrived: make(chan struct{}, 2), proceed: make(chan struct{})}
	create := command.NewCreateWorkHandler(time.Now, agents, barrier, sessions, auth, creationModels(), &creationCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), factory)
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for _, creation := range creations {
		go func() {
			id, err := create.Handle(ctx, creation)
			results <- result{id, err}
		}()
	}
	for range 2 {
		select {
		case <-barrier.arrived:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(barrier.proceed)
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	require.NoError(t, a.err)
	require.Error(t, b.err)
	require.True(t, errors.Is(b.err, domain.ErrConcurrentModification) || errors.Is(b.err, command.ErrStartRequestConflict), "unexpected loser error: %v", b.err)
	require.Equal(t, int32(1), starts.Load())
	winner, err := sessions.Get(ctx, a.id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, winner.State)
	require.True(t, winner.HoldsProcess())
	accepted, found, err := sessions.FindAdmittedStart(ctx, requestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, agentquery.AdmittedStart{WorkID: winner.ID, Kind: agentquery.StartKindStart}, accepted)
	loserCreation := creations[0]
	if winner.AgentID == loserCreation.AgentID {
		loserCreation = creations[1]
	}
	loser, err := sessions.FindWork(ctx, loserCreation.AgentID, loserCreation.Subject)
	require.NoError(t, err)
	require.Nil(t, loser, "losing initial admission must roll back the entire Work")
}

// Independent definitions must both admit their first Work; range locks on an
// empty holding set must not make one creation fail with a database deadlock.
func TestConcurrentIndependentDefinitionsAdmitTheirFirstWorks(t *testing.T) {
	db, definitions, works, first := startingCreationDatabase(t)
	second, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"second-owner"},
		Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "second-owner"},
		HomeBinding: domain.HomeBinding{OwnerID: "second-owner", Namespace: "personal"},
	})
	require.NoError(t, err)
	require.NoError(t, definitions.Save(t.Context(), second))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	barrier := admissionSaveBarrier{AgentWorkRepository: works, arrived: make(chan struct{}, 2), proceed: make(chan struct{})}
	execution := &creationExecution{db: db}
	create := command.NewCreateWorkHandler(time.Now, definitions, barrier, works, infrastructure.NewWorkAuthorizer(db), creationModels(), &creationCatalog{}, creationConnections{}, works, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(execution))
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for _, definition := range []*domain.AgentDefinition{first, second} {
		go func() {
			request := creationInput(definition.ID, "first")
			request.StartingUserID = definition.HomeBinding.OwnerID
			request.SubjectID = definition.HomeBinding.OwnerID
			id, err := create.Handle(ctx, request)
			results <- result{id, err}
		}()
	}
	for range 2 {
		select {
		case <-barrier.arrived:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(barrier.proceed)
	ids := map[string]bool{}
	for range 2 {
		select {
		case r := <-results:
			require.NoError(t, r.err)
			work, err := works.Get(ctx, r.id)
			require.NoError(t, err)
			require.Equal(t, domain.WorkStarting, work.State)
			ids[work.ID] = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.Len(t, ids, 2)
	require.EqualValues(t, 2, execution.starts.Load())
}
