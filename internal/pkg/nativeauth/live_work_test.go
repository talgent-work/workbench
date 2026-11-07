package nativeauth

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
)

// liveAgent stands in for Agent: it verifies one live sandbox's credential
// and reports its Work.
type liveAgent struct {
	agentprivatev1connect.UnimplementedAgentServiceHandler
	mu        sync.Mutex
	live      bool
	subject   *agentprivatev1.WorkSubject
	projectID string
	// acting is Agent's verdict on whether the Work acts.
	acting bool
	asked  []bool
}

func (a *liveAgent) VerifyWorkCredential(_ context.Context, r *connect.Request[agentprivatev1.VerifyWorkCredentialRequest]) (*connect.Response[agentprivatev1.VerifyWorkCredentialResponse], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Header().Get("Authorization") != "Bearer control" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if !a.live || r.Msg.GetCredential() != "sandbox-credential" {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	a.asked = append(a.asked, r.Msg.GetRequireActing())
	if r.Msg.GetRequireActing() && !a.acting {
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	return connect.NewResponse(&agentprivatev1.VerifyWorkCredentialResponse{WorkId: r.Msg.GetWorkId(), SubjectId: "owner", AgentId: "agent", ProjectId: a.projectID, Subject: a.subject, Purpose: "work"}), nil
}

func liveClient(t *testing.T, agent *liveAgent) *LiveWorkClient {
	t.Helper()
	_, handler := agentprivatev1connect.NewAgentServiceHandler(agent)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewLiveWorkClient(server.Client(), server.URL, "control")
	require.NoError(t, err)
	return client
}

// A Work's live sandbox may read before its first turn: Agent answers its
// Work, optional Project and Subject, never the caller.
func TestLiveWorkResolvesTheWorkOfALiveSandbox(t *testing.T) {
	agent := &liveAgent{live: true, subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "case-1"}, projectID: "p1"}
	process, err := liveClient(t, agent).VerifyNativeProcess(context.Background(), "work", "sandbox-credential")
	require.NoError(t, err)
	require.Equal(t, "p1", process.ProjectID)
	require.Equal(t, "owner", process.SubjectID)
	require.Equal(t, "case", process.WorkSubjectKind)
	require.Equal(t, "case-1", process.WorkSubjectID)
	require.Equal(t, "work", process.Purpose)
	require.Equal(t, []bool{false}, agent.asked, "a read does not require the Work to act")

	personal, err := liveClient(t, &liveAgent{live: true}).VerifyNativeProcess(context.Background(), "work", "sandbox-credential")
	require.NoError(t, err, "a Work without a Project or Subject is resolved too")
	require.Empty(t, personal.ProjectID)
	require.Empty(t, personal.WorkSubjectKind)
}

func TestLiveWorkRejectsRetiredOrForgedCredentials(t *testing.T) {
	retired := liveClient(t, &liveAgent{live: false})
	_, err := retired.VerifyNativeProcess(context.Background(), "work", "sandbox-credential")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "a retired sandbox's credential is revoked")

	live := liveClient(t, &liveAgent{live: true})
	_, err = live.VerifyNativeProcess(context.Background(), "work", "guess")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = live.VerifyNativeProcess(context.Background(), "", "sandbox-credential")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// A process acts only while Agent says its Work acts; Agent's refusal reads
// as an unauthenticated action.
func TestActingWorkAdmitsOnlyWhatAgentSaysActs(t *testing.T) {
	working := &liveAgent{live: true, acting: true}
	process, err := NewActingWorkClient(liveClient(t, working)).VerifyNativeProcess(context.Background(), "work", "sandbox-credential")
	require.NoError(t, err)
	require.Equal(t, "work", process.WorkID)
	require.Equal(t, []bool{true}, working.asked, "an action requires the Work to act")

	idle := &liveAgent{live: true}
	_, err = NewActingWorkClient(liveClient(t, idle)).VerifyNativeProcess(context.Background(), "work", "sandbox-credential")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "a Work that does not act is refused")
	_, err = liveClient(t, idle).VerifyNativeProcess(context.Background(), "work", "sandbox-credential")
	require.NoError(t, err, "it may still read")
}
