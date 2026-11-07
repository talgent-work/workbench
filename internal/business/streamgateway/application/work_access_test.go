package application_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
	"github.com/talgent-work/contracts/gen/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// recordingAccess admits every reader and records the admission it was given.
type recordingAccess struct {
	mu         sync.Mutex
	admissions map[string]query.ReaderAdmission
	denied     map[string]bool
	// starting is the Work's current state an admitted stream starts from.
	starting *query.WorkState
}

func (r *recordingAccess) AdmitStreamReader(ctx context.Context, reader string, admission query.ReaderAdmission, work string) (*query.WorkState, error) {
	if err := r.AuthorizeRead(ctx, reader, admission, work); err != nil {
		return nil, err
	}
	return r.starting, nil
}

func (r *recordingAccess) AuthorizeRead(_ context.Context, reader string, admission query.ReaderAdmission, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.admissions[reader] = admission
	if r.denied[reader] {
		return query.ErrWorkAccessDenied
	}
	return nil
}

func (r *recordingAccess) deny(reader string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied[reader] = true
}

func (f *fakeStream) publishAccess(sessionID string, payload *query.WorkAccessPayload) {
	f.mu.Lock()
	sub := f.subs[sessionID]
	f.mu.Unlock()
	if sub != nil {
		sub.ch <- payload
	}
}

func watchAs(t *testing.T, client streamgatewaypublicv1connect.StreamGatewayServiceClient, ctx context.Context, reader string, admission string) *watched {
	t.Helper()
	request := &streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, "test")
	info.RequestHeader().Set(authn.HeaderDelegatedUserID, reader)
	if admission != "" {
		info.RequestHeader().Set(authn.HeaderReaderAdmission, admission)
	}
	stream, err := watch(client.WatchStream(ctx, request))
	require.NoError(t, err)
	return stream
}

func newAccessServer(t *testing.T) (*fakeStream, *recordingAccess, streamgatewaypublicv1connect.StreamGatewayServiceClient) {
	t.Helper()
	store := &fakeStore{rows: []*query.Message{unifiedMessage("m1", "w1", time.Unix(1, 0))}}
	stream := newFakeStream()
	access := &recordingAccess{admissions: map[string]query.ReaderAdmission{}, denied: map[string]bool{}}
	app := application.NewApplication(query.NewWatchStream(store, stream, slogDiscard(t), access), query.NewListMessagesBySessionHandler(store, access), store, access)
	app.Queries.WatchStream.SetSettleDelay(0)
	app.Queries.WatchStream.SetReadmissionInterval(20 * time.Millisecond)
	mux := http.NewServeMux()
	path, handler := "/"+streamgatewaypublicv1connect.StreamGatewayServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		streamgatewaypublicv1connect.RegisterStreamGatewayServiceHandler(s, streamrpc.NewHandler(app))
	}, authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))
	mux.Handle(path, handler)
	srv, httpClient := newH2CTestServer(t, mux)
	return stream, access, streamgatewaypublicv1connect.NewStreamGatewayServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, srv.URL)))
}

func receiveReplay(t *testing.T, stream *watched) {
	t.Helper()
	require.True(t, stream.Receive(), "replay: %v", stream.Err())
	require.Equal(t, "m1", stream.Msg().GetMessage().GetId())
}

func requireWithdrawn(t *testing.T, stream *watched) {
	t.Helper()
	require.True(t, stream.Receive(), "withdrawal frame: %v", stream.Err())
	require.Equal(t, "w1", stream.Msg().GetAccessWithdrawn().GetAgentWorkId(), "withdrawal is a distinct frame, not a connection error")
	require.False(t, stream.Receive(), "the stream ends after the withdrawal")
	require.NoError(t, stream.Err())
}

func TestRevokedWorkShareEndsOnlyThatReadersStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	live, _, client := newAccessServer(t)
	revoked := watchAs(t, client, ctx, "reader", "")
	defer revoked.Close()
	kept := watchAs(t, client, ctx, "owner", "")
	defer kept.Close()
	receiveReplay(t, revoked)
	receiveReplay(t, kept)

	live.publishAccess("w1", &query.WorkAccessPayload{RevokedReaderID: "reader"})
	requireWithdrawn(t, revoked)

	live.publish("w1", unifiedMessage("m2", "w1", time.Unix(2, 0)))
	require.True(t, kept.Receive(), "other readers keep reading: %v", kept.Err())
	require.Equal(t, "m2", kept.Msg().GetMessage().GetId())
}

func TestMakingAWorkPrivateEndsStreamsOfReadersItNoLongerAdmits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	live, access, client := newAccessServer(t)
	member := watchAs(t, client, ctx, "member", "project=project-a")
	defer member.Close()
	share := watchAs(t, client, ctx, "reader", "")
	defer share.Close()
	receiveReplay(t, member)
	receiveReplay(t, share)
	access.mu.Lock()
	require.Equal(t, query.ReaderAdmission{Reference: "project", Value: "project-a"}, access.admissions["member"], "a trusted caller's admission reaches the access decision")
	access.mu.Unlock()

	live.publishAccess("w1", &query.WorkAccessPayload{MadePrivate: true, RetainedReaderIDs: []string{"owner", "reader"}})
	requireWithdrawn(t, member)
	live.publish("w1", unifiedMessage("m2", "w1", time.Unix(2, 0)))
	require.True(t, share.Receive(), "a Work Share holder keeps reading: %v", share.Err())
	require.Equal(t, "m2", share.Msg().GetMessage().GetId())
}

// A withdrawal event can be lost; the stream re-admits its reader on an
// interval, so access Agent no longer grants still ends the stream.
func TestStreamEndsWhenReadmissionIsDeniedWithoutAWithdrawalEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, access, client := newAccessServer(t)
	revoked := watchAs(t, client, ctx, "reader", "")
	defer revoked.Close()
	receiveReplay(t, revoked)

	access.deny("reader")
	requireWithdrawn(t, revoked)
}

func TestClientCannotSupplyItsOwnReaderAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, access, client := newAccessServer(t)
	request := &streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
	info.RequestHeader().Set(authn.HeaderSubject, "290572ed-90f8-50b8-a98e-9cdbac589df3")
	info.RequestHeader().Set(authn.HeaderReaderAdmission, "project=project-a")
	stream, err := watch(client.WatchStream(ctx, request))
	require.NoError(t, err)
	defer stream.Close()
	receiveReplay(t, stream)
	access.mu.Lock()
	defer access.mu.Unlock()
	require.Equal(t, query.ReaderAdmission{}, access.admissions["290572ed-90f8-50b8-a98e-9cdbac589df3"], "only a delegated service call carries an admission")
}

// An admitted reader's stream starts from the Work's current state, SubAgents
// included, before any replay; a refused reader receives nothing.
func TestAnAdmittedStreamStartsFromTheWorksCurrentState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	live, access, client := newAccessServer(t)
	access.starting = &query.WorkState{AgentWorkID: "w1", State: "working", SubAgents: []query.SubAgent{
		{ID: "a", Type: "Explore", Task: "map the code", Status: "completed"},
		{ID: "b", Task: "review", Status: "running"},
	}}
	stream := watchAs(t, client, ctx, "owner", "")
	defer stream.Close()
	require.True(t, stream.Receive(), "starting state: %v", stream.Err())
	state := stream.Msg().GetWorkState()
	require.Equal(t, "working", state.GetState())
	require.Len(t, state.GetSubagents(), 2)
	require.Equal(t, "Explore", state.GetSubagents()[0].GetType())
	require.Equal(t, "map the code", state.GetSubagents()[0].GetTask())
	require.Equal(t, "completed", state.GetSubagents()[0].GetStatus())
	require.Equal(t, "running", state.GetSubagents()[1].GetStatus())
	receiveReplay(t, stream)

	live.publishWorkState("w1", &query.WorkState{AgentWorkID: "w1", State: "working"})
	require.True(t, stream.Receive(), "live state: %v", stream.Err())
	require.Empty(t, stream.Msg().GetWorkState().GetSubagents(), "a later frame replaces them as a whole")

	access.deny("intruder")
	refused := watchAs(t, client, ctx, "intruder", "")
	defer refused.Close()
	require.False(t, refused.Receive())
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(refused.Err()))
}
