//go:build integration

package infrastructure_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func TestSessionOriginRPCPreservesAttributionAfterCloseAndReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dsn := mysqltest.Database(t)
	open := func() *xorm.Engine {
		db, err := xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	db := open()
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	auth := infrastructure.NewWorkAuthorizer(db)
	ids := map[string]string{}
	definitions := map[string]string{}
	for _, purpose := range []string{"work", "case_coordinator"} {
		definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: purpose, UsageScope: domain.UsageScope{Kind: "employee", Reference: purpose}, HomeBinding: domain.HomeBinding{OwnerID: purpose, Namespace: "personal"}})
		require.NoError(t, err)
		require.NoError(t, agents.Save(ctx, definition))
		id, err := command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, auth, creationModels(), &creationCatalog{}, creationConnections{}, sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{})).Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), AgentID: definition.ID, SubjectID: "owner", Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkspaceReference: "workspace-" + purpose})
		require.NoError(t, err)
		ids[purpose], definitions[purpose] = id, definition.ID
	}
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, sessions, exitedProcess, infrastructure.NewExecutionRequestsFactory(executionPort{}), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutionExited, command.ComputeReclaimed} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ids["work"]}))
	}
	require.NoError(t, command.NewCloseWorkHandler(sessions, auth, infrastructure.NewExecutionRequestsFactory(&recoveryExecutionPort{}), commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: ids["work"], RequestID: "close"}))
	require.NoError(t, db.Close())
	origins := infrastructure.NewWorkQueryRepository(open())
	app := &application.Application{Queries: application.Queries{GetWorkOrigin: query.NewGetWorkOriginHandler(origins), ResolveWorkStarts: query.NewResolveWorkStartsHandler(origins)}}
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterAgentServiceHandler(s, transport.NewAgentHandler(app, "control"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	authorized, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer control")
	for purpose, id := range ids {
		request := &agentprivatev1.GetWorkOriginRequest{WorkId: id}
		response, err := client.GetWorkOrigin(authorized, request)
		require.NoError(t, err)
		require.Equal(t, id, response.GetWorkId())
		require.Equal(t, definitions[purpose], response.GetAgentId())
		require.Equal(t, purpose, response.GetPurpose())
		require.Equal(t, "project", response.GetProjectId())
		require.Equal(t, "case", response.GetSubject().GetId())
	}
	request := &agentprivatev1.GetWorkOriginRequest{WorkId: ids["work"]}
	_, err := client.GetWorkOrigin(ctx, request)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	request.WorkId = "missing"
	_, err = client.GetWorkOrigin(authorized, request)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// Work starts resolve in one read for the Project's Works; a missing Work
	// and every Work asked about under another Project are unknown.
	starts := func(project string) []*agentprivatev1.ResolvedWorkStart {
		request := &agentprivatev1.ResolveWorkStartsRequest{ProjectId: project, WorkIds: []string{ids["work"], ids["case_coordinator"], "missing"}}
		ctx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set("Authorization", "Bearer control")
		response, err := client.ResolveWorkStarts(ctx, request)
		require.NoError(t, err)
		return response.GetWorks()
	}
	known := starts("project")
	require.Len(t, known, 3)
	for index, id := range []string{ids["work"], ids["case_coordinator"]} {
		require.Equal(t, id, known[index].GetWorkId())
		require.True(t, known[index].GetKnown())
		require.Equal(t, "owner", known[index].GetStartedBy())
		require.Positive(t, known[index].GetStartedAtUnixMs())
	}
	require.False(t, known[2].GetKnown())
	for _, work := range starts("other-project") {
		require.False(t, work.GetKnown())
		require.Empty(t, work.GetStartedBy())
	}
	unauthenticated := &agentprivatev1.ResolveWorkStartsRequest{ProjectId: "project", WorkIds: []string{ids["work"]}}
	_, err = client.ResolveWorkStarts(ctx, unauthenticated)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	retained, err := infrastructure.NewAgentWorkRepository(open(), time.Now).Get(ctx, ids["work"])
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, retained.State, "attribution reads cannot restart or reopen the source")
}
