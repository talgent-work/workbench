//go:build integration

package account_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
	"github.com/talgent-work/contracts/gen/go/account/public/v1/accountpublicv1connect"
	accountconnect "github.com/talgent-work/workbench/internal/business/account/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
)

// applicationService serves Account's public Application procedures behind
// the admission interceptor, as the controlplane mounts them.
func (h *harness) applicationService() accountpublicv1connect.ApplicationServiceClient {
	h.t.Helper()
	server := connectserver.New(slog.New(slog.NewTextHandler(io.Discard, nil)), authn.NewPrincipalInterceptor())
	accountpublicv1connect.RegisterApplicationServiceHandler(server, accountconnect.NewApplications(h.app))
	mux := http.NewServeMux()
	connectserver.Mount(mux, server)
	rpc := httptest.NewServer(mux)
	h.t.Cleanup(rpc.Close)
	return accountpublicv1connect.NewApplicationServiceClient(connect.NewClient(connecthttp.NewTransport(rpc.Client(), rpc.URL)))
}

func newPerson() string { return uuid.Must(uuid.NewV7()).String() }

// as speaks for a principal the way the entry writes a verified identity.
func as(principal, subject string) context.Context {
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
	info.RequestHeader().Set(authn.HeaderPrincipal, principal)
	info.RequestHeader().Set(authn.HeaderSubject, subject)
	return ctx
}

// grant requests a token with the client credentials grant, the client
// authenticating with HTTP Basic.
func (h *harness) grant(clientID, secret string) (int, map[string]any) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/auth/token", strings.NewReader(url.Values{"grant_type": {"client_credentials"}}.Encode()))
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	resp, err := h.server.Client().Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	var body map[string]any
	require.NoError(h.t, json.NewDecoder(resp.Body).Decode(&body))
	return resp.StatusCode, body
}

func TestApplicationRegistrationShowsItsSecretOnceAndKeepsADigest(t *testing.T) {
	h := newHarness(t)
	owner := newPerson()
	service := h.applicationService()

	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: " Support Desk "})
	require.NoError(t, err)
	application := registered.GetApplication()
	require.Equal(t, "Support Desk", application.GetName())
	secret := registered.GetClientSecret()
	require.True(t, strings.HasPrefix(secret, "wbs_"))
	require.Len(t, application.GetClientSecrets(), 1)
	listed := application.GetClientSecrets()[0]
	require.Equal(t, accountpublicv1.ClientSecretState_CLIENT_SECRET_STATE_CURRENT, listed.GetState())
	require.Equal(t, secret[len(secret)-4:], listed.GetHint())
	require.Zero(t, listed.GetLastUsedAtUnixMs())

	rows, err := h.engine.QueryString("SELECT secret_digest FROM application_client_secret")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotContains(t, rows[0]["secret_digest"], strings.TrimPrefix(secret, "wbs_"), "the secret itself is never stored")
	require.Len(t, rows[0]["secret_digest"], 64)

	read, err := service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: application.GetId()})
	require.NoError(t, err)
	require.Equal(t, application.GetId(), read.GetApplication().GetId())
}

func TestApplicationOwnersListOnlyTheirOwnApplications(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner, other := newPerson(), newPerson()
	first, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	h.clock.Advance(time.Second)
	second, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "CI Triage"})
	require.NoError(t, err)
	_, err = service.RegisterApplication(as("user", other), &accountpublicv1.RegisterApplicationRequest{Name: "Elsewhere"})
	require.NoError(t, err)

	listed, err := service.ListOwnedApplications(as("user", owner), &accountpublicv1.ListOwnedApplicationsRequest{})
	require.NoError(t, err)
	var names []string
	for _, application := range listed.GetApplications() {
		names = append(names, application.GetName())
		require.Len(t, application.GetClientSecrets(), 1)
	}
	require.Equal(t, []string{"Support Desk", "CI Triage"}, names)
	require.Equal(t, first.GetApplication().GetId(), listed.GetApplications()[0].GetId())
	require.Equal(t, second.GetApplication().GetId(), listed.GetApplications()[1].GetId())

	_, err = service.GetApplication(as("user", other), &accountpublicv1.GetApplicationRequest{ApplicationId: first.GetApplication().GetId()})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another person cannot tell it exists")
	_, err = service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: "not-an-id"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestApplicationRegistrationRefusesABadNameAndNonPeople(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	_, err := service.RegisterApplication(as("user", newPerson()), &accountpublicv1.RegisterApplicationRequest{Name: "   "})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	registered, err := service.RegisterApplication(as("user", newPerson()), &accountpublicv1.RegisterApplicationRequest{Name: "Desk"})
	require.NoError(t, err)
	_, err = service.ListOwnedApplications(as("application", registered.GetApplication().GetId()), &accountpublicv1.ListOwnedApplicationsRequest{})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "an Application never manages Applications")
}

func TestClientCredentialsGrantIssuesAnApplicationToken(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	h.clock.Advance(time.Minute)

	status, body := h.grant(id, registered.GetClientSecret())
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "Bearer", body["token_type"])
	require.Equal(t, applicationTokenLifetime.Seconds(), body["expires_in"])
	token := h.verify(body["access_token"].(string))
	subject, _ := token.Subject()
	require.Equal(t, id, subject)
	var principal string
	require.NoError(t, token.Get("principal", &principal))
	require.Equal(t, "application", principal)
	for _, claim := range []string{"sid", "preferred_username", "email", "agent_id", "for"} {
		require.False(t, token.Has(claim), claim)
	}
	expires, _ := token.Expiration()
	require.Equal(t, h.clock.Now().Add(applicationTokenLifetime).Unix(), expires.Unix())

	read, err := service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	require.Equal(t, h.clock.Now().UnixMilli(), read.GetApplication().GetClientSecrets()[0].GetLastUsedAtUnixMs(), "the grant records the secret's use")
}

func TestClientCredentialsGrantRefusesWrongCredentialsAsAnInvalidClient(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	registered, err := service.RegisterApplication(as("user", newPerson()), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	other, err := service.RegisterApplication(as("user", newPerson()), &accountpublicv1.RegisterApplicationRequest{Name: "Other"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()

	for name, credentials := range map[string][2]string{
		"another application's secret": {id, other.GetClientSecret()},
		"a malformed secret":           {id, "wbs_guess"},
		"an unknown client":            {newPerson(), registered.GetClientSecret()},
		"a malformed client":           {"desk", registered.GetClientSecret()},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := h.grant(credentials[0], credentials[1])
			require.Equal(t, http.StatusUnauthorized, status)
			require.Equal(t, "invalid_client", body["error"])
			require.NotContains(t, body, "access_token")
		})
	}
	read, err := service.ListOwnedApplications(as("user", newPerson()), &accountpublicv1.ListOwnedApplicationsRequest{})
	require.NoError(t, err)
	require.Empty(t, read.GetApplications())
}
