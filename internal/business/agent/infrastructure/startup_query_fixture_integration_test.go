//go:build integration

package infrastructure_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentrpc "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"xorm.io/xorm"
)

func startupRPCClient(t *testing.T, db *xorm.Engine) agentprivatev1connect.AgentServiceClient {
	t.Helper()
	app := &application.Application{Queries: application.Queries{GetWorkStartup: query.NewGetWorkStartupHandler(infrastructure.NewExecutionQueryRepository(db), infrastructure.NewWorkAuthorizer(db), infrastructure.NewContinuationRequests(db, time.Now))}}
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterAgentServiceHandler(s, agentrpc.NewAgentHandler(app, "control"))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
}
