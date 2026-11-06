//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/pkg/mysqltest"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

func TestAggregatePersistencePreservesAdmissionAndConversationAcrossReconnect(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Original instructions", StartupAgent: "review:architect", RequiredPlugins: []string{"review"}}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, agent))
	require.Zero(t, agent.Version, "Save must not change the loaded version")
	first, err := agents.Get(ctx, agent.ID)
	require.NoError(t, err)
	second, err := agents.Get(ctx, agent.ID)
	require.NoError(t, err)
	require.NoError(t, first.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "First change"}))
	require.NoError(t, second.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "Second change"}))
	require.NoError(t, agents.Save(ctx, first))
	require.Equal(t, 1, first.Version)
	require.ErrorIs(t, agents.Save(ctx, second), domain.ErrConcurrentModification)
	require.NoError(t, (&application.Application{Definitions: agents}).ChangeAgentDefinitionCapabilities(ctx, application.ChangeAgentDefinitionCapabilities{AgentID: agent.ID, Capabilities: agent.Capabilities}))
	agent, err = agents.Get(ctx, agent.ID)
	require.NoError(t, err)
	require.Equal(t, "Original instructions", agent.Capabilities.Prompt)
	require.Equal(t, domain.UsageScope{Kind: "employee", Reference: "employee"}, agent.UsageScope)
	require.Equal(t, domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, agent.HomeBinding)
	_, err = agent.ChangeSoul("owner", "Answer with the conclusion first.")
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, agent))
	agent, err = agents.Get(ctx, agent.ID)
	require.NoError(t, err)
	require.Equal(t, "Answer with the conclusion first.", agent.Soul)

	traceAttributes := map[string]string{"talgent.project_id": "project-1", "talgent.owner_id": "case"}
	id, err := command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, infrastructure.NewWorkAuthorizer(db), creationModels(), &repositoryCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{})).Handle(ctx, command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), SubjectID: "owner", AgentID: agent.ID, WorkspaceReference: "workspace-a", TraceAttributes: traceAttributes})
	require.NoError(t, err)
	session, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	for key, value := range traceAttributes {
		require.Equal(t, value, session.TraceAttributes[key])
	}
	require.Equal(t, "Answer with the conclusion first.", session.HarnessProcess.Soul, "the first start took up the Soul, retained with its process")
	originalCapabilities := session.CapabilitySnapshot.Clone()

	authorizer := infrastructure.NewWorkAuthorizer(db)
	require.NoError(t, authorizer.AuthorizeWork(ctx, "owner", session.ID))
	require.ErrorIs(t, authorizer.AuthorizeWork(ctx, "Owner", session.ID), access.ErrAccessDenied)
	require.ErrorIs(t, authorizer.AuthorizeWork(ctx, "intruder", session.ID), access.ErrAccessDenied)
	require.ErrorIs(t, authorizer.AuthorizeWork(ctx, "owner", "missing"), access.ErrAccessDenied)
	require.NoError(t, authorizer.AuthorizeWork(ctx, "owner", session.ID))
	contextRef := session.HarnessSessionReference
	require.NoError(t, (&application.Application{Definitions: agents}).ChangeAgentDefinitionCapabilities(ctx, application.ChangeAgentDefinitionCapabilities{AgentID: agent.ID, Capabilities: domain.CapabilityConfiguration{Prompt: "New instructions"}}))

	reload := func() *domain.AgentWork {
		value, err := sessions.Get(ctx, id)
		require.NoError(t, err)
		return value
	}
	session = reload()
	require.Equal(t, "Original instructions", session.CapabilitySnapshot.Prompt)
	require.Equal(t, "review:architect", session.CapabilitySnapshot.StartupAgent)
	port := executionPort{}
	current, err := commandtest.UnchangedPlugins{}.CurrentReleases(ctx, "owner", originalCapabilities.Plugins)
	require.NoError(t, err)
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true, CurrentPlugins: current}
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.Save(ctx, session))
	require.ErrorIs(t, sessions.Save(ctx, session), domain.ErrConcurrentModification)
	session = reload()
	require.True(t, session.HoldsProcess(), "an admitted start holds a Harness process before readiness")
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.Save(ctx, session))
	session = reload()
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.NoError(t, sessions.Save(ctx, session))
	session = reload()
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, sessions.Save(ctx, session))
	session = reload()
	require.Equal(t, domain.WorkStopping, session.State)
	require.False(t, session.HoldsProcess(), "the exited process is no longer held")
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, sessions.Save(ctx, session))

	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
	authorizer = infrastructure.NewWorkAuthorizer(db)
	require.NoError(t, authorizer.AuthorizeWork(ctx, "owner", id))
	require.ErrorIs(t, authorizer.AuthorizeWork(ctx, "intruder", id), access.ErrAccessDenied)
	session = reload()
	require.Equal(t, domain.WorkStopped, session.State)
	require.Equal(t, contextRef, session.HarnessSessionReference)
	require.True(t, session.HarnessProcess.Exited)
	require.True(t, session.HarnessProcess.ComputeReclaimed)
	require.Equal(t, domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, session.HomeBinding)
	require.Equal(t, originalCapabilities, session.CapabilitySnapshot, "reconnection retains exact releases, declarations and effective typed values")
	require.Equal(t, "strict", session.CapabilitySnapshot.Plugins[0].Configuration["mode"])
	require.NoError(t, session.Resume(ctx, admission, port))
	require.NoError(t, sessions.Save(ctx, session))
	session = reload()
	require.Equal(t, id, session.ID)
	require.Equal(t, contextRef, session.HarnessSessionReference, "Resume continues the same Harness Session")
	require.Equal(t, domain.ContextContinue, session.HarnessProcess.ContextMode)
	require.False(t, session.HarnessProcess.Exited, "the resumed process replaces the exited process facts")
	require.Equal(t, "Original instructions", session.CapabilitySnapshot.Prompt)
	require.Equal(t, "review:architect", session.CapabilitySnapshot.StartupAgent)
	require.Equal(t, "workspace-a", session.WorkspaceReference)
}

type creationRaceLookup struct {
	delegate domain.WorkLookupPort
	calls    atomic.Int32
	arrived  chan struct{}
	release  chan struct{}
	workers  int32
}

func (p *creationRaceLookup) LocateWork(ctx context.Context, workID string) (*domain.AgentWork, error) {
	return p.delegate.LocateWork(ctx, workID)
}

func (p *creationRaceLookup) FindWork(ctx context.Context, agentID string, subject domain.WorkSubject) (*domain.AgentWork, error) {
	session, err := p.delegate.FindWork(ctx, agentID, subject)
	if p.calls.Add(1) <= p.workers {
		select {
		case p.arrived <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return session, err
}

func TestConcurrentSubjectCreationReturnsOneRetainedWork(t *testing.T) {
	setup := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(setup, time.Minute)
	defer cancel()
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: domain.HomeBinding{OwnerID: "employee-home", Namespace: "personal"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Base", StartupAgent: "base:member", RequiredPlugins: []string{"base"}}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, agent))
	const workers = 8
	lookup := &creationRaceLookup{delegate: sessions, workers: workers, arrived: make(chan struct{}, workers), release: make(chan struct{})}
	handler := command.NewCreateWorkHandler(time.Now, agents, sessions, lookup, infrastructure.NewWorkAuthorizer(db), creationModels(), &repositoryCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	request := command.CreateWork{RequestID: uuid.NewString(), SubjectID: "owner", AgentID: agent.ID, Subject: domain.WorkSubject{Kind: "case", ID: "Work-A"}, ProjectID: "project-a", WorkspaceReference: "work-one", TraceAttributes: map[string]string{"talgent.project_id": "project-a"}}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, workers)
	var group sync.WaitGroup
	for i := range workers {
		group.Go(func() {
			attempt := request
			attempt.RequestID = uuid.NewString()
			prompt, startup := fmt.Sprintf("Prompt-%d", i), fmt.Sprintf("review:role-%d", i)
			attempt.CapabilityDifferences = domain.CapabilityDifferences{Prompt: &prompt, StartupAgent: &startup, Plugins: []domain.PluginChoice{{Slug: "review", Include: true, Configuration: pluginconfig.Configuration{"mode": fmt.Sprint(i)}}}}
			id, callErr := handler.Handle(ctx, attempt)
			results <- result{id: id, err: callErr}
		})
	}
	for range workers {
		select {
		case <-lookup.arrived:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(lookup.release)
	group.Wait()
	close(results)
	var originalID string
	for result := range results {
		require.NoError(t, result.err)
		require.NotEmpty(t, result.id)
		if originalID == "" {
			originalID = result.id
		}
		require.Equal(t, originalID, result.id)
	}
	rows, err := db.QueryString("SELECT id FROM agent_session WHERE agent_id = ? AND deleted_at = 0", agent.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "concurrent requests must not leave extra conversations")
	original, err := sessions.Get(ctx, originalID)
	require.NoError(t, err)
	require.Equal(t, domain.HomeBinding{OwnerID: "employee-home", Namespace: "personal"}, original.HomeBinding)
	require.NotContains(t, original.TraceAttributes, "talgent.user_id", "a delegated technical principal is not automatically a human employee")
	require.Len(t, original.CapabilitySnapshot.Plugins, 2)
	var winning int
	_, err = fmt.Sscanf(original.CapabilitySnapshot.Prompt, "Prompt-%d", &winning)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("review:role-%d", winning), original.CapabilitySnapshot.StartupAgent)
	require.Equal(t, pluginconfig.Configuration{"mode": fmt.Sprint(winning)}, original.CapabilitySnapshot.Plugins[1].Configuration)
	require.NoError(t, (&application.Application{Definitions: agents}).ChangeAgentDefinitionCapabilities(ctx, application.ChangeAgentDefinitionCapabilities{AgentID: agent.ID, Capabilities: domain.CapabilityConfiguration{Prompt: "Changed", RequiredPlugins: []string{"base"}, StartupAgent: "base:member"}}))
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
	handler = command.NewCreateWorkHandler(time.Now, infrastructure.NewAgentDefinitionRepository(db, time.Now), sessions, sessions, infrastructure.NewWorkAuthorizer(db), creationModels(), &repositoryCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	request.WorkspaceReference = "another-authorized-workspace"
	request.CapabilityDifferences = domain.CapabilityDifferences{Plugins: []domain.PluginChoice{{Slug: "duplicate", Include: true}, {Slug: "duplicate", Include: true}}}
	id, err := handler.Handle(ctx, request)
	require.NoError(t, err, "the existing Work of the Subject wins without comparing new capability parameters")
	require.Equal(t, originalID, id)
	retained, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, original.CapabilitySnapshot, retained.CapabilitySnapshot)
	require.Equal(t, original.HarnessSessionReference, retained.HarnessSessionReference)
	require.Equal(t, "work-one", retained.WorkspaceReference)
	request.SubjectID = "intruder"
	_, err = handler.Handle(ctx, request)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	request.SubjectID = "owner"
	stopRetainedWork := func() {
		t.Helper()
		work, err := sessions.Get(ctx, originalID)
		require.NoError(t, err)
		require.NoError(t, work.ConfirmPrepared(context.Background(), time.Now(), nil))
		require.NoError(t, work.ConfirmStarted(context.Background(), time.Now(), nil))
		require.NoError(t, work.Stop(ctx, uuid.NewString(), executionPort{}))
		require.NoError(t, work.RecordExit(ctx, domain.HarnessExit{}, executionPort{}))
		require.NoError(t, work.ConfirmComputeReclaimed())
		require.NoError(t, sessions.Save(ctx, work))
	}
	stopRetainedWork()
	verifyConcurrentAdmission(t, db, originalID)
	stopRetainedWork()
	request.RequestID = uuid.NewString()
	request.Subject, request.ProjectID = domain.WorkSubject{Kind: "case", ID: "Work-B"}, "project-b"
	request.CapabilityDifferences = domain.CapabilityDifferences{}
	otherID, err := handler.Handle(ctx, request)
	require.NoError(t, err)
	require.NotEqual(t, originalID, otherID)
	other, err := sessions.Get(ctx, otherID)
	require.NoError(t, err)
	require.Equal(t, "Changed", other.CapabilitySnapshot.Prompt)
	require.Equal(t, original.HomeBinding, other.HomeBinding)
	require.NotEqual(t, original.HarnessSessionReference, other.HarnessSessionReference)
}

func verifyConcurrentAdmission(t *testing.T, db *xorm.Engine, sessionID string) {
	t.Helper()
	ctx := context.Background()
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	var starts atomic.Int32
	port := startupExecutionPort{onStart: func(domain.StartExecutionRequest) error { starts.Add(1); return nil }}
	handler := command.NewResumeWorkHandler(time.Now, sessions, agents, infrastructure.NewWorkAuthorizer(db), computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(port), sessions, infrastructure.NewContinuationRequests(db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{WorkID: sessionID, SubjectID: "owner", RequestID: uuid.Must(uuid.NewV7()).String()}
	start := make(chan struct{})
	failures := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			<-start
			var err error
			for range 4 {
				err = handler.Handle(ctx, request)
				if !errors.Is(err, domain.ErrConcurrentModification) {
					break
				}
			}
			failures <- err
		})
	}
	close(start)
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), starts.Load(), "competing retries authorize only one outbound execution")
	rows, err := db.QueryString("SELECT id, kind FROM agent_start_request WHERE session_id = ? AND kind = ?", sessionID, "resume")
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{"id": request.RequestID, "kind": "resume"}}, rows)
	_, err = command.NewRestartWorkHandler(time.Now, sessions, agents, infrastructure.NewWorkAuthorizer(db), computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(port), sessions, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, request)
	require.ErrorIs(t, err, command.ErrStartRequestConflict, "reusing an acceptance identity for another operation is rejected")
}

// Only the external catalog is controlled; snapshot composition, validation and
// persistence run through the real Agent application and repositories.
type repositoryCatalog struct{ creationCatalog }

func (*repositoryCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	return []domain.PersonalPlugin{{Slug: "review", Enabled: false}}, nil
}

func (*repositoryCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	agents := []domain.AgentComponent{{Name: slug + ":member"}, {Name: slug + ":architect"}}
	for i := range 8 {
		agents = append(agents, domain.AgentComponent{Name: fmt.Sprintf("%s:role-%d", slug, i)})
	}
	return domain.PluginRelease{Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:" + slug + "-1", NativeEntry: []byte(fmt.Sprintf(`{"name":%q,"version":"1"}`, slug)), Agents: agents, Parameters: pluginconfig.ParameterDeclarations{"mode": {Type: "string", Title: "Mode", Description: "Review mode", Default: "strict"}}}, nil
}

func (*repositoryCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
