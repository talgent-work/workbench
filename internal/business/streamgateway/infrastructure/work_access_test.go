package infrastructure_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

type workReaders struct {
	agentprivatev1connect.UnimplementedWorkAccessServiceHandler
	t        *testing.T
	calls    int
	admitted *agentprivatev1.ReaderAdmission
}

func (a *workReaders) AdmitWorkReader(ctx context.Context, request *agentprivatev1.AdmitWorkReaderRequest) (*agentprivatev1.AdmitWorkReaderResponse, error) {
	a.calls++
	info, _ := connect.CallInfoForServerContext(ctx)
	require.Equal(a.t, "Bearer control", info.RequestHeader().Get("Authorization"))
	require.Equal(a.t, "viewer", request.GetReaderId())
	a.admitted = request.GetAdmission()
	switch request.GetWorkId() {
	case "denied":
		return nil, connect.NewError(connect.CodePermissionDenied, query.ErrWorkAccessDenied.Error()).WithCause(query.ErrWorkAccessDenied)
	case "offline":
		return nil, connect.NewError(connect.CodeUnavailable, query.ErrWorkAccessUnavailable.Error()).WithCause(query.ErrWorkAccessUnavailable)
	}
	response := &agentprivatev1.AdmitWorkReaderResponse{Basis: agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE}
	if request.GetIncludeWorkState() {
		response.WorkState = &streamgatewaypublicv1.WorkStateFrame{AgentWorkId: request.GetWorkId(), State: "working", Subagents: []*agentpublicv1.SubAgent{{Id: "a", Type: "Plan", Task: "plan it", Status: "failed"}}}
	}
	return response, nil
}

func TestWorkMessageAccessAsksAgentFromItsOwnFacts(t *testing.T) {
	api := &workReaders{t: t}
	handler := connecttest.Handler(func(s *connect.Server) { agentprivatev1connect.RegisterWorkAccessServiceHandler(s, api) })
	server := httptest.NewServer(handler)
	defer server.Close()
	access, err := infrastructure.NewWorkAccess(server.Client(), server.URL, "control")
	require.NoError(t, err)
	ctx := context.Background()
	require.ErrorIs(t, access.AuthorizeRead(ctx, "", query.ReaderAdmission{}, "work"), query.ErrWorkAccessDenied)
	require.Zero(t, api.calls)
	require.NoError(t, access.AuthorizeRead(ctx, "viewer", query.ReaderAdmission{}, "shared"))
	require.Empty(t, api.admitted.GetWorkCaller().GetKind(), "a direct reader carries no admission")
	require.NoError(t, access.AuthorizeRead(ctx, "viewer", query.ReaderAdmission{WorkCallerKind: "project", WorkCallerID: "project-a"}, "public"))
	require.Equal(t, "project-a", api.admitted.GetWorkCaller().GetId(), "a trusted caller's admission reaches Agent")
	require.ErrorIs(t, access.AuthorizeRead(ctx, "viewer", query.ReaderAdmission{}, "denied"), query.ErrWorkAccessDenied)
	require.ErrorIs(t, access.AuthorizeRead(ctx, "viewer", query.ReaderAdmission{}, "offline"), query.ErrWorkAccessUnavailable)

	state, err := access.AdmitStreamReader(ctx, "viewer", query.ReaderAdmission{}, "shared")
	require.NoError(t, err)
	require.Equal(t, &query.WorkState{AgentWorkID: "shared", State: "working", SubAgents: []query.SubAgent{{ID: "a", Type: "Plan", Task: "plan it", Status: "failed"}}}, state, "a stream starts from the state Agent answers")
	_, err = access.AdmitStreamReader(ctx, "viewer", query.ReaderAdmission{}, "denied")
	require.ErrorIs(t, err, query.ErrWorkAccessDenied)
}
