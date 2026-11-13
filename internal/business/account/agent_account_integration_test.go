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

// issueRequest names a Work started in the name of a project, found again by
// its case and reading and publishing within that case.
func issueRequest(work, project, represented string) *accountprivatev1.IssueWorkIdentityTokenRequest {
	return &accountprivatev1.IssueWorkIdentityTokenRequest{
		WorkId: work, WorkCallerKind: "project", WorkCallerId: project, AgentId: "agent-1", Purpose: "case-work",
		WorkKeyKind: "case", WorkKeyId: "case-1", FileHomeKind: "case", FileHomeId: "case-1", RepresentedUserId: represented,
	}
}

// standaloneRequest names an Application's Standalone Work: the Application is
// its Work Caller and owner, and it has no Work Key or File Home.
func standaloneRequest(work, application string) *accountprivatev1.IssueWorkIdentityTokenRequest {
	return &accountprivatev1.IssueWorkIdentityTokenRequest{
		WorkId: work, WorkCallerKind: "application", WorkCallerId: application, AgentId: "agent-1", Purpose: "work", ApplicationId: application,
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
	for _, name := range []string{"principal", "work_caller", "work_key", "file_home", "agent_id", "purpose", "for"} {
		var value string
		require.NoError(t, token.Get(name, &value), name)
		claims[name] = value
	}
	require.Equal(t, map[string]string{"principal": "agent", "work_caller": "project:project-1", "work_key": "case:case-1", "file_home": "case:case-1",
		"agent_id": "agent-1", "purpose": "case-work", "for": person}, claims)
	for _, gone := range []string{"project_id", "work_subject_kind", "work_subject_id"} {
		require.False(t, token.Has(gone), "an agent token states no %s", gone)
	}
	require.False(t, token.Has("sid"), "an agent token names no Login Session")
	expires, _ := token.Expiration()
	require.Equal(t, h.clock.Now().Add(agentTokenLifetime).Unix(), expires.Unix(), "valid seven days")
	require.Equal(t, expires.Unix(), resp.GetExpiresAt().AsTime().Unix())

	rows, err := h.engine.QueryString("SELECT * FROM agent_account WHERE work_id = ?", work)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the first request created the Agent Account")
	stored := rows[0]
	require.Equal(t, []string{"project", "project-1", "case", "case-1", "case", "case-1"},
		[]string{stored["work_caller_kind"], stored["work_caller_id"], stored["work_key_kind"], stored["work_key_id"], stored["file_home_kind"], stored["file_home_id"]},
		"the Agent Account keeps the Work's Work Caller, Work Key and File Home")
	for column := range rows[0] {
		require.NotContains(t, column, "secret", "Account stores no agent secret")
		require.NotContains(t, column, "token")
	}

	// A later start issues a new token for the same facts, read back from the
	// stored Agent Account; the earlier one stays valid until it expires.
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

	for name, drift := range map[string]func(*accountprivatev1.IssueWorkIdentityTokenRequest){
		"another Work Caller": func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.WorkCallerId = "project-2" },
		"another Work Key":    func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.WorkKeyId = "case-2" },
		"no File Home":        func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.FileHomeKind, r.FileHomeId = "", "" },
	} {
		request := issueRequest(work, "project-1", "")
		drift(request)
		_, err = client.IssueWorkIdentityToken(asAgent(context.Background()), request)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), name)
	}
}

// A Work's Work Caller is required, a Work Key or File Home is a kind with an
// identity or nothing, and Account carries each exactly or refuses it.
func TestAgentAccountRefusesIncompleteWorkFacts(t *testing.T) {
	h := newHarness(t)
	client := h.agentAccounts()
	for name, incomplete := range map[string]func(*accountprivatev1.IssueWorkIdentityTokenRequest){
		"no Work Caller":         func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.WorkCallerKind, r.WorkCallerId = "", "" },
		"a Work Caller's kind":   func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.WorkCallerId = "" },
		"a kind with a colon":    func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.WorkKeyKind = "case:x" },
		"a File Home's identity": func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.FileHomeKind = "" },
		"a padded Work Caller":   func(r *accountprivatev1.IssueWorkIdentityTokenRequest) { r.WorkCallerId = " project-1" },
	} {
		work := newWorkID()
		request := issueRequest(work, "project-1", "")
		incomplete(request)
		_, err := client.IssueWorkIdentityToken(asAgent(context.Background()), request)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
		rows, err := h.engine.QueryString("SELECT work_id FROM agent_account WHERE work_id = ?", work)
		require.NoError(t, err)
		require.Empty(t, rows, "%s opens no Agent Account", name)
	}
}

// A Standalone Work states its Work Caller and no Work Key or File Home.
func TestAgentAccountForAStandaloneWork(t *testing.T) {
	h := newHarness(t)
	application := newWorkID()
	resp, err := h.agentAccounts().IssueWorkIdentityToken(asAgent(context.Background()), standaloneRequest(newWorkID(), application))
	require.NoError(t, err)
	token := h.verify(resp.GetToken())
	var caller string
	require.NoError(t, token.Get("work_caller", &caller))
	require.Equal(t, "application:"+application, caller)
	require.False(t, token.Has("work_key"), "no Work Key, no claim")
	require.False(t, token.Has("file_home"), "no File Home, no claim")
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
	request := standaloneRequest(work, application)
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
	request := standaloneRequest(newWorkID(), newWorkID())
	request.WorkLabels = labels
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
