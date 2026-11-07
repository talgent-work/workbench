//go:build integration

package infrastructure_test

import (
	"context"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"xorm.io/xorm"
	"xorm.io/xorm/contexts"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"google.golang.org/protobuf/proto"
)

// controlRequest is the context of a call under the control token.
func controlRequest(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer control")
	return ctx
}

// Agent-only schema, real private HTTP and list SQL. Selection is by generic
// the Project; the caller composes its own facts and authorizes them.
func TestWorkListsSelectAdmittedWorksByProject(t *testing.T) {
	const owner = "123456789012345680"
	f := newWorkDefinitionFixture(t, owner)
	ctx := context.Background()
	principal := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db))
	reader := infrastructure.NewWorkQueryRepository(f.db)
	app := &application.Application{WorkViews: reader, Queries: application.Queries{ListWorks: query.NewListWorksHandler(reader)}}
	api := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterWorkFactsServiceHandler(s, transport.NewWorkFactsHandler(app, "control"))
	})
	server := httptest.NewServer(api)
	defer server.Close()
	client := agentprivatev1connect.NewWorkFactsServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	feedback := newRuns(t, f.db, f.sessions)
	initial := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	project := uuid.NewString()
	makeSession := func(projectID, key string, stopped bool) string {
		creation := f.request
		preparation := *creation.InitialExecution
		preparation.RequestID, preparation.Input.ID = uuid.NewString(), uuid.NewString()
		preparation.WorkBranch = "talgent/" + key
		creation.InitialExecution = &preparation
		creation.RequestID = preparation.RequestID
		creation.WorkspaceReference = uuid.NewString()
		creation.Subject, creation.ProjectID = domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, projectID
		id, e := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, principal, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels())).Handle(ctx, creation)
		require.NoError(t, e)
		if stopped {
			request, e := initial.Get(ctx, id)
			require.NoError(t, e)
			request.Submitted, request.InputPrepared = true, true
			require.NoError(t, initial.Save(ctx, request))
			require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))
			for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
				require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: id}))
			}
			require.NoError(t, command.NewStopWorkHandler(f.sessions, principal, executionsOf(f.db)).Handle(ctx, command.WorkControl{SubjectID: owner, WorkID: id, RequestID: uuid.NewString()}))
			feedback.exit(ctx, id)
			feedback.release(ctx, id)
		}
		return id
	}
	first := makeSession(project, "TAL-1", true)
	second := makeSession(project, "TAL-2", true)
	other := makeSession(uuid.NewString(), "OTHER-1", true)
	starting := makeSession(project, "TAL-3", false)
	continued, err := continuedRef(second, command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, principal, computertest.Checked(startupAuthority{}), infrastructure.NewContinuationRequests(f.db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: owner, WorkID: second, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted, command.UserInteractionRequired} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: continued, InteractionID: "list-question", ObservationSequence: 1000, RequiredInteractionIDs: []string{"list-question"}}))
	}
	for id, updated := range map[string]int64{first: 2000, second: 3000, other: 4000, starting: 5000} {
		_, err = f.db.Exec("UPDATE agent_session SET updated_at=?,created_at=? WHERE id=?", updated, 1000, id)
		require.NoError(t, err)
	}

	_, err = client.ListWorks(ctx, &agentprivatev1.ListWorksRequest{AgentOwner: owner})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "only a trusted caller lists Works")
	_, err = client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "an unbounded list of every Work is never served")

	inProject, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{ProjectId: project, Purpose: "work"})
	require.NoError(t, err)
	expected := []string{first, second, starting}
	sort.Strings(expected)
	require.Equal(t, expected, workFactIDs(inProject.Works))
	for _, work := range inProject.Works {
		expectedState := "stopped"
		if work.WorkId == second {
			expectedState = "working"
		} else if work.WorkId == starting {
			expectedState = "starting"
		}
		require.Equal(t, expectedState, work.State)
		require.Equal(t, owner, work.AgentOwner)
		require.Equal(t, project, work.ProjectId)
		require.Equal(t, "case", work.Subject.GetKind(), "the caller's Subject comes back uninterpreted")
		require.Empty(t, work.WorkProgress, "lists carry no detail beyond collaboration facts")
	}
	// A Working Work whose Turn awaits its owner's answer is listed as Waiting
	// beside its state.
	_, err = f.db.Exec("UPDATE agent_session SET harness_process=JSON_SET(harness_process, '$.waiting_since', ?) WHERE id=?", "2026-10-15T00:00:00Z", second)
	require.NoError(t, err)
	inProject, err = client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{ProjectId: project, Purpose: "work"})
	require.NoError(t, err)
	for _, work := range inProject.Works {
		require.Equal(t, work.WorkId == second, work.Waiting, work.WorkId)
	}
	foreignPurpose, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{ProjectId: project, Purpose: "project_coordination"})
	require.NoError(t, err)
	require.Empty(t, foreignPurpose.Works, "purpose selects the definition, opaquely")
	page, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{ProjectId: project, After: expected[0], Limit: 1})
	require.NoError(t, err)
	require.Equal(t, expected[1:2], workFactIDs(page.Works))
	session, err := f.sessions.Get(ctx, first)
	require.NoError(t, err)
	require.Equal(t, project, session.ProjectID)
	byID, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{WorkIds: []string{starting, other}})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{starting, other}, workFactIDs(byID.Works))
	recent, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{AgentOwner: owner, ProjectId: project, Recent: true, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{starting}, workFactIDs(recent.Works))

	// A Work Share holder's Works are found within one Project, for departure.
	shared, err := f.sessions.Get(ctx, second)
	require.NoError(t, err)
	definition, err := f.agents.Get(ctx, shared.AgentID)
	require.NoError(t, err)
	_, err = shared.GrantWorkShare(definition, owner, "reader", domain.ReaderAdmission{ProjectID: project})
	require.NoError(t, err)
	require.NoError(t, f.sessions.Save(ctx, shared))
	_, err = f.db.Exec("UPDATE agent_session SET updated_at=3000 WHERE id=?", second)
	require.NoError(t, err)
	readers, err := reader.ListWorkViews(ctx, query.WorkListFilter{ProjectID: project, ShareReader: "reader", Limit: 10})
	require.NoError(t, err)
	require.Len(t, readers, 1)
	require.Equal(t, second, readers[0].WorkID)

	// An older archived Work remains discoverable even when newer live Works fill
	// the requested page. Filtering must precede LIMIT.
	require.NoError(t, command.NewCloseWorkHandler(f.sessions, principal, executionsOf(f.db), infrastructure.NewContinuationRequests(f.db, time.Now), &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: owner, WorkID: first, RequestID: uuid.NewString()}))
	_, err = f.db.Exec("UPDATE agent_session SET updated_at=500 WHERE id=?", first)
	require.NoError(t, err)
	archived, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{AgentOwner: owner, ProjectId: project, Recent: true, Limit: 1, Archived: proto.Bool(true)})
	require.NoError(t, err)
	require.Equal(t, []string{first}, workFactIDs(archived.Works))
	require.Equal(t, "closed", archived.Works[0].State)
	live, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{AgentOwner: owner, ProjectId: project, Recent: true, Archived: proto.Bool(false)})
	require.NoError(t, err)
	require.Equal(t, []string{starting, second}, workFactIDs(live.Works))
	_, err = f.db.Exec("UPDATE agent_session SET deleted_at=1 WHERE id=?", other)
	require.NoError(t, err)
	mine, err := client.ListWorks(controlRequest(ctx), &agentprivatev1.ListWorksRequest{AgentOwner: owner})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{first, second, starting}, workFactIDs(mine.Works), "lists exclude logically deleted Works")

	// Populate Works outside this Project to distinguish indexed access from a
	// coincidentally cheap scan of the initial few rows.
	insertStartingWorks(t, f.db, f.request.AgentID, owner, 128, 1000, uuid.NewString)
	_, err = f.db.Exec("ANALYZE TABLE agent_session,agent_definition")
	require.NoError(t, err)
	capture := &workListSQLCapture{}
	f.db.AddHook(capture)
	for _, filter := range []query.WorkListFilter{
		{AgentOwner: owner, ProjectID: project, Purpose: "work", Recent: true, Limit: 20, Archived: proto.Bool(true)},
		{AgentOwner: owner, Purpose: "work", Recent: true, Limit: 20},
		{ProjectID: project, Purpose: "work", Limit: 500},
		{WorkIDs: []string{first}, Limit: 100},
	} {
		_, err = reader.ListWorkViews(ctx, filter)
		require.NoError(t, err)
		require.NotEmpty(t, capture.sql)
		rows, err := f.db.QueryString(append([]any{"EXPLAIN " + capture.sql}, capture.args...)...)
		require.NoError(t, err)
		t.Logf("work list plan for %+v: %+v", filter, rows)
		for _, row := range rows {
			if row["table"] != "s" {
				continue // A definition is reached by its primary key or is a one-row table.
			}
			require.NotEqual(t, "ALL", row["type"], "Work list reads must retain indexed access")
			require.NotEmpty(t, row["key"])
		}
	}
}

// insertStartingWorks writes n Starting Works of one definition as rows, for
// tests that need volume rather than the creation path.
func insertStartingWorks(t *testing.T, db *xorm.Engine, agentID, startedBy string, n int, createdAt int64, project func() string) {
	t.Helper()
	values := make([]string, n)
	args := make([]any, 0, 8*n)
	for i := range values {
		values[i] = "(?, ?, ?, 'case', ?, ?, 1, '{}', '', '', '{}', '{}', '', ?, 'personal', 1, ?, ?, 0)"
		args = append(args, uuid.NewString(), agentID, startedBy, uuid.NewString(), project(), startedBy, createdAt, createdAt)
	}
	_, err := db.Exec(append([]any{"INSERT INTO agent_session (id, agent_id, started_by, subject_kind, subject_id, project_ref, state, capabilities, context_ref, workspace_ref, trace_attributes, harness_process, failure_reason, home_owner_id, home_namespace, version, created_at, updated_at, deleted_at) VALUES " + strings.Join(values, ",")}, args...)...)
	require.NoError(t, err)
}

func workFactIDs(values []*agentprivatev1.WorkFacts) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.WorkId)
	}
	return ids
}

// Capture the actual production query; EXPLAIN does not use a copied SELECT.
type workListSQLCapture struct {
	sql  string
	args []any
}

func (h *workListSQLCapture) BeforeProcess(c *contexts.ContextHook) (context.Context, error) {
	if strings.HasPrefix(c.SQL, "SELECT") && strings.Contains(c.SQL, "agent_session") && strings.Contains(c.SQL, "agent_owner") {
		h.sql = c.SQL
		h.args = append([]any(nil), c.Args...)
	}
	return c.Ctx, nil
}
func (*workListSQLCapture) AfterProcess(*contexts.ContextHook) error { return nil }
