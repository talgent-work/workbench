//go:build integration

package infrastructure_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentrpc "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"xorm.io/xorm"
)

func startupRPCClient(t *testing.T, db *xorm.Engine) agentprivatev1connect.AgentServiceClient {
	t.Helper()
	app := &application.Application{Queries: application.Queries{GetWorkStartup: query.NewGetWorkStartupHandler(infrastructure.NewExecutionQueryRepository(db), infrastructure.NewWorkAuthorizer(db), infrastructure.NewContinuationRequests(db, time.Now))}}
	_, handler := agentprivatev1connect.NewAgentServiceHandler(agentrpc.NewAgentHandler(app, "control"))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL)
}
