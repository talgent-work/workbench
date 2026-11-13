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
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
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
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners)
	ids := map[string]string{}
	definitions := map[string]string{}
	for _, purpose := range []string{"work", "case_coordinator"} {
		definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: purpose, UsageScope: domain.UsageScope{Kind: "employee", Reference: purpose}})
		require.NoError(t, err)
		require.NoError(t, agents.Save(ctx, definition))
		id, err := command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, agents, sessions, auth, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(db, creationModels())).Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), AgentID: definition.ID, SubjectID: "owner", WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}})
		require.NoError(t, err)
		ids[purpose], definitions[purpose] = id, definition.ID
	}
	feedback := newRuns(t, db, sessions)
	feedback.exit(ctx, ids["work"])
	feedback.release(ctx, ids["work"])
	require.NoError(t, command.NewCloseWorkHandler(sessions, auth, feedback.executions, &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: ids["work"], RequestID: "close"}))
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
		require.Equal(t, "project", response.GetWorkCaller().GetKind())
		require.Equal(t, "project", response.GetWorkCaller().GetId())
		require.Equal(t, "case", response.GetWorkKey().GetId())
		require.Nil(t, response.GetFileHome(), "a Work without a File Home answers none")
	}
	request := &agentprivatev1.GetWorkOriginRequest{WorkId: ids["work"]}
	_, err := client.GetWorkOrigin(ctx, request)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	request.WorkId = "missing"
	_, err = client.GetWorkOrigin(authorized, request)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// Work starts resolve in one read for the Work Caller's Works; a missing
	// Work and every Work asked about under another Work Caller are unknown.
	starts := func(project string) []*agentprivatev1.ResolvedWorkStart {
		request := &agentprivatev1.ResolveWorkStartsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: project}, WorkIds: []string{ids["work"], ids["case_coordinator"], "missing"}}
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
	unauthenticated := &agentprivatev1.ResolveWorkStartsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: "project"}, WorkIds: []string{ids["work"]}}
	_, err = client.ResolveWorkStarts(ctx, unauthenticated)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	retained, err := infrastructure.NewAgentWorkRepository(open(), time.Now).Get(ctx, ids["work"])
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, retained.State, "attribution reads cannot restart or reopen the source")
}
