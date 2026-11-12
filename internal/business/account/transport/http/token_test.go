package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

type grantRequest struct {
	contentType string
	body        string
	basicID     string
	basicSecret string
}

func grantWith(t *testing.T, app *fakeApp, request grantRequest) (*http.Response, map[string]any) {
	t.Helper()
	app.now = time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	handler, err := NewHandler(app, Config{LoginAttemptKey: []byte(strings.Repeat("k", 32)), LoginAttemptTTL: time.Minute, Now: func() time.Time { return app.now }},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/auth/token", strings.NewReader(request.body))
	contentType := request.contentType
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	req.Header.Set("Content-Type", contentType)
	if request.basicID != "" {
		req.SetBasicAuth(url.QueryEscape(request.basicID), url.QueryEscape(request.basicSecret))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resp := rec.Result()
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	return resp, body
}

func TestTokenGrantIssuesABearerTokenForBasicCredentials(t *testing.T) {
	app := &fakeApp{}
	resp, body := grantWith(t, app, grantRequest{body: "grant_type=client_credentials", basicID: "client", basicSecret: "wbs_secret"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, map[string]any{"access_token": "signed", "token_type": "Bearer", "expires_in": float64(900)}, body)
	require.Equal(t, []application.IssueApplicationIdentityToken{{ClientID: "client", ClientSecret: "wbs_secret"}}, app.grants)
}

func TestTokenGrantAcceptsCredentialsInTheBody(t *testing.T) {
	app := &fakeApp{}
	resp, _ := grantWith(t, app, grantRequest{body: "grant_type=client_credentials&client_id=client&client_secret=wbs_secret"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []application.IssueApplicationIdentityToken{{ClientID: "client", ClientSecret: "wbs_secret"}}, app.grants)
}

func TestTokenGrantRefusesAMismatchedSecretAsAnInvalidClient(t *testing.T) {
	resp, body := grantWith(t, &fakeApp{grant: domain.ErrClientSecretMismatch}, grantRequest{body: "grant_type=client_credentials", basicID: "client", basicSecret: "wrong"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_client", body["error"])
	require.Equal(t, `Basic realm="talgent"`, resp.Header.Get("WWW-Authenticate"))

	resp, body = grantWith(t, &fakeApp{grant: domain.ErrClientSecretMismatch}, grantRequest{body: "grant_type=client_credentials&client_id=client&client_secret=wrong"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_client", body["error"])
	require.Empty(t, resp.Header.Get("WWW-Authenticate"), "only a Basic client is challenged")
}

func TestTokenGrantRefusesMalformedRequestsWithoutIssuing(t *testing.T) {
	for name, tc := range map[string]struct {
		request grantRequest
		status  int
		code    string
	}{
		"json body":         {grantRequest{contentType: "application/json", body: "{}"}, http.StatusBadRequest, "invalid_request"},
		"no grant type":     {grantRequest{body: "client_id=c&client_secret=s"}, http.StatusBadRequest, "invalid_request"},
		"other grant":       {grantRequest{body: "grant_type=password&client_id=c&client_secret=s"}, http.StatusBadRequest, "unsupported_grant_type"},
		"scope":             {grantRequest{body: "grant_type=client_credentials&scope=all&client_id=c&client_secret=s"}, http.StatusBadRequest, "invalid_scope"},
		"repeated":          {grantRequest{body: "grant_type=client_credentials&client_id=c&client_id=d&client_secret=s"}, http.StatusBadRequest, "invalid_request"},
		"both methods":      {grantRequest{body: "grant_type=client_credentials&client_id=c&client_secret=s", basicID: "c", basicSecret: "s"}, http.StatusBadRequest, "invalid_request"},
		"no client":         {grantRequest{body: "grant_type=client_credentials"}, http.StatusUnauthorized, "invalid_client"},
		"secret without id": {grantRequest{body: "grant_type=client_credentials&client_secret=s"}, http.StatusUnauthorized, "invalid_client"},
	} {
		t.Run(name, func(t *testing.T) {
			app := &fakeApp{}
			resp, body := grantWith(t, app, tc.request)
			require.Equal(t, tc.status, resp.StatusCode)
			require.Equal(t, tc.code, body["error"])
			require.Empty(t, app.grants)
		})
	}
}

func TestTokenGrantAnswersUnavailableWhenAccountFails(t *testing.T) {
	resp, body := grantWith(t, &fakeApp{grant: errors.New("database unavailable")}, grantRequest{body: "grant_type=client_credentials&client_id=c&client_secret=s"})
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Equal(t, "temporarily_unavailable", body["error"])
	require.NotContains(t, body["error_description"], "database")
}
