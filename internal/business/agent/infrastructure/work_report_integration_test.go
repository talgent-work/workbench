//go:build integration

package infrastructure_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

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
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"google.golang.org/protobuf/types/known/timestamppb"
	"xorm.io/xorm"
)

func TestWorkReportRetainsAtomicHistoryAndSelectsByGenericScope(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	ctx := context.Background()
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	observed := end.Add(-25 * time.Hour)
	clock := func() time.Time { return observed }
	sessions := infrastructure.NewAgentWorkRepository(f.db, clock)
	authorization := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners)
	id, err := command.NewCreateWorkHandler(time.Now, f.agents, sessions, authorization, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels())).Handle(ctx, f.request)
	require.NoError(t, err)
	observed = observed.Add(15 * time.Minute)
	feedback := newRunsAt(t, f.db, sessions, clock)
	feedback.ready(ctx, id)
	require.NoError(t, command.NewInputAcceptance(sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))
	observed = end.Add(-23 * time.Hour)
	closeWork := command.NewCloseWorkHandler(sessions, authorization, feedback.executions, &commandtest.InputBuffer{})
	require.NoError(t, closeWork.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	observed = end.Add(-22 * time.Hour)
	feedback.exit(ctx, id)
	feedback.release(ctx, id)
	// The close kept while the Work stopped applies once it has stopped.
	require.NoError(t, closeWork.ApplyKeptClose(ctx, id))
	closed, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	// A stale save must not leak a historical observation when its root CAS fails.
	stale := *closed
	require.NoError(t, sessions.Save(ctx, closed))
	require.ErrorIs(t, sessions.Save(ctx, &stale), domain.ErrConcurrentModification)
	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	project := f.request.ProjectID
	observed = end
	report := query.NewGetWorkReportHandler(infrastructure.NewWorkQueryRepository(db), clock)
	app := &application.Application{Queries: application.Queries{GetWorkReport: report}}
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterWorkFactsServiceHandler(s, transport.NewWorkFactsHandler(app, "control"))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := agentprivatev1connect.NewWorkFactsServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	request := &agentprivatev1.GetWorkReportRequest{ProjectId: project, Purpose: "work", Window: "24h", End: timestamppb.New(end)}
	response, err := client.GetWorkReport(controlRequest(ctx), request)
	require.NoError(t, err)
	require.True(t, response.Current.HistoryComplete)
	require.Equal(t, []string{id}, response.Current.EligibleWorkIds, "the caller classifies eligible Works by its own facts")
	require.EqualValues(t, 1, response.Current.GetClosed())
	require.EqualValues(t, 1, response.Previous.GetActive(), "period-end Running snapshot survives later closure and adapter recreation")
	require.EqualValues(t, 0, response.Previous.GetClosed())
	require.EqualValues(t, 1, response.Current.Buckets[1].GetClosed())
	require.Equal(t, 180.0, response.Current.Buckets[1].GetP50Minutes())
	require.Equal(t, "owner", response.Current.Owners[0].Key, "archived Work is reported")
	_, err = client.GetWorkReport(ctx, request)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "only a trusted caller, which authorizes its scope, reads the report")
	rows, err := db.QueryString("EXPLAIN SELECT id FROM agent_session WHERE project_ref=? AND deleted_at=0 AND created_at<?", project, end.UnixMilli())
	require.NoError(t, err)
	require.Contains(t, rows[0]["possible_keys"], "idx_project_ref_deleted_at_created_at_id")
	histories, err := infrastructure.NewWorkQueryRepository(db).ReadWorkHistory(ctx, query.WorkReportScope{ProjectID: project, Purpose: "work"}, end)
	require.NoError(t, err)
	require.Len(t, histories, 1)
	require.Len(t, histories[0].Facts, 5, "Starting, Working, Stopping, Stopped, Closed only; duplicates and failed saves add no facts")
	// Reporting is neither the personal Recent list nor its 100-row page.
	observed = end.Add(-time.Hour)
	create := command.NewCreateWorkHandler(time.Now, f.agents, sessions, authorization, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels()))
	insertStartingWorks(t, f.db, f.request.AgentID, "owner", 104, observed.UnixMilli(), func() string { return project })
	// Another project's Work and same-project coordinator must not enter totals.
	_, err = create.Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), AgentID: f.request.AgentID, SubjectID: "owner", Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: uuid.NewString(), WorkspaceReference: uuid.NewString()})
	require.NoError(t, err)
	coordinator, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Coordinator", Purpose: "case_coordinator", UsageScope: domain.UsageScope{Kind: "project", Reference: project}, HomeBinding: domain.HomeBinding{OwnerID: project, Namespace: "project"}})
	require.NoError(t, err)
	require.NoError(t, f.agents.Save(ctx, coordinator))
	_, err = create.Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), AgentID: coordinator.ID, SubjectID: "owner", Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: project, WorkspaceReference: uuid.NewString()})
	require.NoError(t, err)
	observed = end
	response, err = client.GetWorkReport(controlRequest(ctx), request)
	require.NoError(t, err)
	require.Len(t, response.Current.EligibleWorkIds, 105, "another Project's Work and another purpose's Work stay out")
	require.EqualValues(t, 104, response.Current.GetCreated())
	require.EqualValues(t, 105, response.Current.Owners[0].Count)
	require.EqualValues(t, 1, response.Current.GetClosed())
	// Existing Work can retain its current state without any historical facts.
	_, err = db.Exec("DELETE FROM agent_session_state_history WHERE session_id=?", id)
	require.NoError(t, err)
	response, err = client.GetWorkReport(controlRequest(ctx), request)
	require.NoError(t, err)
	require.True(t, response.Current.HistoryComplete)
	require.EqualValues(t, 104, response.Current.GetCreated())
	require.EqualValues(t, 1, response.Current.GetClosed())
	require.Nil(t, response.Previous.Active, "do not reconstruct previous Running from current Closed")
	require.Nil(t, response.Current.Buckets[1].Closed, "unknown closure time affects the time series only")
	require.EqualValues(t, 105, response.Current.Owners[0].Count)
}
