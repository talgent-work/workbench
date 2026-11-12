//go:build integration

package account_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
	"github.com/talgent-work/contracts/gen/go/account/private/v1/accountprivatev1connect"
	accountconnect "github.com/talgent-work/workbench/internal/business/account/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
)

func newWorkID() string { return uuid.Must(uuid.NewV7()).String() }

// agentAccounts serves Account's private Agent Account procedure as the
// controlplane mounts it.
func (h *harness) agentAccounts() accountprivatev1connect.AgentAccountServiceClient {
	h.t.Helper()
	server := connectserver.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	accountprivatev1connect.RegisterAgentAccountServiceHandler(server, accountconnect.NewAgentAccounts(h.app))
	mux := http.NewServeMux()
	connectserver.Mount(mux, server)
	rpc := httptest.NewServer(mux)
	h.t.Cleanup(rpc.Close)
	return accountprivatev1connect.NewAgentAccountServiceClient(connect.NewClient(connecthttp.NewTransport(rpc.Client(), rpc.URL)))
}

func issueRequest(work, project, represented string) *accountprivatev1.IssueWorkIdentityTokenRequest {
	return &accountprivatev1.IssueWorkIdentityTokenRequest{
		WorkId: work, ProjectId: project, AgentId: "agent-1", Purpose: "case-work",
		WorkSubjectKind: "case", WorkSubjectId: "case-1", RepresentedUserId: represented,
	}
}

func asAgent(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, "agent")
	return ctx
}

func (h *harness) issueAgentToken(work, represented string) string {
	h.t.Helper()
	resp, err := h.agentAccounts().IssueWorkIdentityToken(asAgent(context.Background()), issueRequest(work, "project-1", represented))
	require.NoError(h.t, err)
	return resp.GetToken()
}

func TestAgentAccountIssuesTheWorksAgentToken(t *testing.T) {
	h := newHarness(t)
	client := h.agentAccounts()
	work, person := newWorkID(), newWorkID()

	resp, err := client.IssueWorkIdentityToken(asAgent(context.Background()), issueRequest(work, "project-1", person))
	require.NoError(t, err)
	token := h.verify(resp.GetToken())
	subject, _ := token.Subject()
	require.Equal(t, work, subject)
	claims := map[string]string{}
	for _, name := range []string{"principal", "project_id", "agent_id", "purpose", "work_subject_kind", "work_subject_id", "for"} {
		var value string
		require.NoError(t, token.Get(name, &value), name)
		claims[name] = value
	}
	require.Equal(t, map[string]string{"principal": "agent", "project_id": "project-1", "agent_id": "agent-1", "purpose": "case-work",
		"work_subject_kind": "case", "work_subject_id": "case-1", "for": person}, claims)
	require.False(t, token.Has("sid"), "an agent token names no Login Session")
	expires, _ := token.Expiration()
	require.Equal(t, h.clock.Now().Add(agentTokenLifetime).Unix(), expires.Unix(), "valid seven days")
	require.Equal(t, expires.Unix(), resp.GetExpiresAt().AsTime().Unix())

	rows, err := h.engine.QueryString("SELECT * FROM agent_account WHERE work_id = ?", work)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the first request created the Agent Account")
	for column := range rows[0] {
		require.NotContains(t, column, "secret", "Account stores no agent secret")
		require.NotContains(t, column, "token")
	}

	// A later start issues a new token for the same facts; the earlier one
	// stays valid until it expires.
	h.clock.Advance(time.Hour)
	again, err := client.IssueWorkIdentityToken(asAgent(context.Background()), issueRequest(work, "project-1", person))
	require.NoError(t, err)
	require.NotEqual(t, resp.GetToken(), again.GetToken())
	h.verify(resp.GetToken())
}

func TestAgentAccountRefusesDriftingFacts(t *testing.T) {
	h := newHarness(t)
	client := h.agentAccounts()
	work := newWorkID()
	_, err := client.IssueWorkIdentityToken(asAgent(context.Background()), issueRequest(work, "project-1", ""))
	require.NoError(t, err)

	_, err = client.IssueWorkIdentityToken(asAgent(context.Background()), issueRequest(work, "project-2", ""))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestAgentAccountForAnAgentRepresentingNoOne(t *testing.T) {
	h := newHarness(t)
	token := h.verify(h.issueAgentToken(newWorkID(), ""))
	require.False(t, token.Has("for"), "no represented person, no claim")
}

// An Application's Work names its owning Application in every agent token,
// represents no person, and keeps that owner for good.
func TestAgentAccountStatesTheOwningApplication(t *testing.T) {
	h := newHarness(t)
	client := h.agentAccounts()
	work, application := newWorkID(), newWorkID()
	request := &accountprivatev1.IssueWorkIdentityTokenRequest{WorkId: work, AgentId: "agent-1", Purpose: "work", ApplicationId: application}
	resp, err := client.IssueWorkIdentityToken(asAgent(context.Background()), request)
	require.NoError(t, err)
	token := h.verify(resp.GetToken())
	var owner string
	require.NoError(t, token.Get("application_id", &owner))
	require.Equal(t, application, owner)
	require.False(t, token.Has("for"), "an Application's Work represents no person")

	request.ApplicationId = newWorkID()
	_, err = client.IssueWorkIdentityToken(asAgent(context.Background()), request)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "the owning Application never drifts")
	request.ApplicationId = "not-an-application"
	_, err = client.IssueWorkIdentityToken(asAgent(context.Background()), request)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	unowned := h.verify(h.issueAgentToken(newWorkID(), ""))
	require.False(t, unowned.Has("application_id"), "a Work no Application owns states none")
}

// A Work's Work Labels are among the facts every agent token states, across
// a reload of its Agent Account, and never drift.
func TestAgentAccountStatesTheWorkLabels(t *testing.T) {
	h := newHarness(t)
	client := h.agentAccounts()
	labels := map[string]string{"user": "u123", "tenant": "acme"}
	request := &accountprivatev1.IssueWorkIdentityTokenRequest{WorkId: newWorkID(), AgentId: "agent-1", Purpose: "work", ApplicationId: newWorkID(), WorkLabels: labels}
	for range 2 {
		resp, err := client.IssueWorkIdentityToken(asAgent(context.Background()), request)
		require.NoError(t, err)
		var stated map[string]any
		require.NoError(t, h.verify(resp.GetToken()).Get("work_labels", &stated))
		require.Equal(t, map[string]any{"user": "u123", "tenant": "acme"}, stated)
	}

	request.WorkLabels = map[string]string{"user": "u456", "tenant": "acme"}
	_, err := client.IssueWorkIdentityToken(asAgent(context.Background()), request)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "the Work Labels never drift")

	unlabelled := h.verify(h.issueAgentToken(newWorkID(), ""))
	require.False(t, unlabelled.Has("work_labels"), "a Work without labels states none")
}

func TestAgentAccountServesOnlyTrustedServices(t *testing.T) {
	h := newHarness(t)
	_, err := h.agentAccounts().IssueWorkIdentityToken(context.Background(), issueRequest(newWorkID(), "", ""))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = h.agentAccounts().IssueWorkIdentityToken(asAgent(context.Background()), issueRequest("not-a-work", "", ""))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
