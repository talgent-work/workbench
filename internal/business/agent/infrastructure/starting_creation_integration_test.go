//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"xorm.io/xorm"
)

func TestCreationCommitsStartingAndAdmissionBeforeExecution(t *testing.T) {
	db, definitions, works, agent := startingCreationDatabase(t)
	catalog := &creationCatalog{}
	capacity := &initialCreationCapacity{err: domain.ErrCapacityUnavailable}
	execution := &creationExecution{AgentWorkRepository: works, db: db}
	handler := command.NewCreateWorkHandler(time.Now, definitions, execution, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), creationModels(), commandtest.StartCatalog(catalog, creationConnections{}), computertest.Checked(capacity))
	request := creationInput(agent.ID, "first")
	workID, err := handler.Handle(t.Context(), request)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	require.Empty(t, workID)
	assertCreationRows(t, db, 0)
	require.Zero(t, execution.starts.Load())
	capacity.err = nil
	workID, err = handler.Handle(t.Context(), request)
	require.NoError(t, err)
	work, err := works.Get(t.Context(), workID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.HoldsProcess())
	require.Equal(t, "1", work.CapabilitySnapshot.Plugins[0].Release.Version)
	admitted, found, err := works.FindAdmittedStart(t.Context(), request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, query.AdmittedStart{WorkID: workID, Kind: query.StartKindStart}, admitted)
	require.EqualValues(t, 1, execution.starts.Load())
	assertCreationRows(t, db, 1)
	// Response-loss recovery is lookup-only even if catalog and capacity are down.
	catalog.unavailable = true
	capacity.err = domain.ErrCapacityUnavailable
	before := catalog.reads.Load()
	reused, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, workID, reused)
	require.Equal(t, before, catalog.reads.Load())
	other := creationInput(agent.ID, "other")
	other.RequestID = request.RequestID
	_, err = handler.Handle(t.Context(), other)
	require.ErrorIs(t, err, command.ErrStartRequestConflict)
	assertCreationRows(t, db, 1)
}

func TestCreationReusesSubjectCommittedBeforeAdmissionRejection(t *testing.T) {
	db, definitions, works, agent := startingCreationDatabase(t)
	port := &creationExecution{AgentWorkRepository: works, db: db}
	winner := command.NewCreateWorkHandler(time.Now, definitions, port, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(&initialCreationCapacity{}))
	winning := creationInput(agent.ID, "same-subject")
	var winnerID string
	// The winner commits the same Subject after the loser's lookup, and the
	// loser's capacity check then refuses: the loser returns the winner's Work.
	capacity := &creationBeforeCapacity{before: func() error {
		var err error
		winnerID, err = winner.Handle(t.Context(), winning)
		return err
	}, err: domain.ErrCapacityUnavailable}
	loser := command.NewCreateWorkHandler(time.Now, definitions, port, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(capacity))
	reused, err := loser.Handle(t.Context(), creationInput(agent.ID, "same-subject"))
	require.NoError(t, err)
	require.Equal(t, winnerID, reused)
	assertCreationRows(t, db, 1)
	require.EqualValues(t, 1, port.starts.Load(), "reuse cannot send another initial startup")
}

type creationBeforeCapacity struct {
	before func() error
	err    error
}

func (c *creationBeforeCapacity) AdmitRoom(context.Context) error {
	if c.before != nil {
		before := c.before
		c.before = nil
		if err := before(); err != nil {
			return err
		}
	}
	return c.err
}

func startingCreationDatabase(t *testing.T) (*xorm.Engine, *infrastructure.AgentDefinitionRepository, *infrastructure.AgentWorkRepository, *domain.AgentDefinition) {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, Capabilities: domain.CapabilityConfiguration{RequiredPlugins: []string{"runtime"}}})
	require.NoError(t, err)
	require.NoError(t, definitions.Save(t.Context(), agent))
	agent, err = definitions.Get(t.Context(), agent.ID)
	require.NoError(t, err)
	return db, definitions, works, agent
}

func creationInput(agentID, reference string) command.CreateWork {
	return command.CreateWork{StartingUserID: "owner", RequestID: uuid.NewString(), SubjectID: "owner", AgentID: agentID, WorkspaceReference: reference, Subject: domain.WorkSubject{Kind: "case", ID: reference}, ProjectID: "project"}
}
func creationModels() query.ModelConfigurations {
	return query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "fixture"}})
}
func assertCreationRows(t *testing.T, db *xorm.Engine, want int) {
	t.Helper()
	for _, table := range []string{"agent_session", "agent_start_request"} {
		var counts []int
		require.NoError(t, db.SQL("SELECT COUNT(*) FROM "+table).Find(&counts))
		require.Equal(t, []int{want}, counts, table)
	}
}

type initialCreationCapacity struct{ err error }

func (c *initialCreationCapacity) AdmitRoom(context.Context) error { return c.err }

type creationCatalog struct {
	reads       atomic.Int32
	unavailable bool
}

func (c *creationCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	c.reads.Add(1)
	if c.unavailable {
		return nil, errors.New("catalog unavailable")
	}
	return nil, nil
}
func (c *creationCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	return domain.PluginRelease{Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:runtime-1", NativeEntry: []byte(`{"name":"runtime","version":"1"}`)}, nil
}

// creationExecution observes, in place of beginning the execution, each
// start a committed Work admitted, and checks that both durable records are
// readable by a separate database session by then. No fake repository
// substitutes for the commit.
type creationExecution struct {
	command.AgentWorkRepository
	db     *xorm.Engine
	starts atomic.Int32
}

func (e *creationExecution) RegisterWork(ctx context.Context, work *domain.AgentWork, registration command.WorkRegistration) error {
	if err := e.AgentWorkRepository.RegisterWork(ctx, work, registration); err != nil {
		return err
	}
	return e.started(ctx, work)
}

func (e *creationExecution) CommitStart(ctx context.Context, work *domain.AgentWork, start query.AdmittedStart, requestID string) error {
	if err := e.AgentWorkRepository.CommitStart(ctx, work, start, requestID); err != nil {
		return err
	}
	return e.started(ctx, work)
}

func (e *creationExecution) Save(ctx context.Context, work *domain.AgentWork) error {
	if err := e.AgentWorkRepository.Save(ctx, work); err != nil {
		return err
	}
	return e.started(ctx, work)
}

func (e *creationExecution) started(ctx context.Context, work *domain.AgentWork) error {
	for _, event := range work.Events.Drain() {
		if _, admitted := event.(domain.WorkStartAdmitted); !admitted {
			continue
		}
		rows, err := e.db.Context(ctx).QueryString("SELECT s.state,s.holds_process,r.kind FROM agent_session s JOIN agent_start_request r ON r.session_id=s.id WHERE s.id=?", work.ID)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0]["state"] != "1" || rows[0]["holds_process"] != "1" || rows[0]["kind"] != "start" {
			return errors.New("execution preceded atomic creation admission")
		}
		e.starts.Add(1)
	}
	return nil
}

func (*creationCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
