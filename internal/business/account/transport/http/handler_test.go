package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/account/application"
)

// fakeApp answers sign-out with a fixed outcome; the endpoints delegate once.
type fakeApp struct {
	signOut  error
	signOuts []string
	grant    error
	grants   []application.IssueApplicationIdentityToken
	now      time.Time
}

func (f *fakeApp) SignInURL(string, string) string { return "https://github.test/authorize" }
func (f *fakeApp) CompleteGitHubSignIn(context.Context, application.CompleteGitHubSignIn) (application.SignedIn, error) {
	return application.SignedIn{}, errors.New("unused")
}
func (f *fakeApp) RefreshLoginSession(context.Context, application.RefreshLoginSession) (application.SignedIn, error) {
	return application.SignedIn{}, errors.New("unused")
}
func (f *fakeApp) SignOut(_ context.Context, token string) error {
	f.signOuts = append(f.signOuts, token)
	return f.signOut
}
func (f *fakeApp) PublishedKeys(context.Context) ([]byte, error) { return []byte(`{"keys":[]}`), nil }
func (f *fakeApp) IssueApplicationIdentityToken(_ context.Context, cmd application.IssueApplicationIdentityToken) (application.IssuedToken, error) {
	f.grants = append(f.grants, cmd)
	if f.grant != nil {
		return application.IssuedToken{}, f.grant
	}
	return application.IssuedToken{Token: "signed", ExpiresAt: f.now.Add(15 * time.Minute)}, nil
}

func signOutWith(t *testing.T, app *fakeApp, cookieDomain ...string) *http.Response {
	t.Helper()
	cfg := Config{LoginAttemptKey: []byte(strings.Repeat("k", 32)), LoginAttemptTTL: time.Minute}
	if len(cookieDomain) > 0 {
		cfg.CookieDomain = cookieDomain[0]
	}
	handler, err := NewHandler(app, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: AccessCookie, Value: "token"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

func clearedCookies(resp *http.Response) []string {
	var names []string
	for _, cookie := range resp.Cookies() {
		if cookie.MaxAge < 0 {
			names = append(names, cookie.Name)
		}
	}
	return names
}

func TestSignOutClearsTheDeviceOnlyAfterRevocation(t *testing.T) {
	app := &fakeApp{}
	resp := signOutWith(t, app)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.ElementsMatch(t, []string{AccessCookie, RefreshCookie}, clearedCookies(resp))
	require.Equal(t, []string{"token"}, app.signOuts)
}

func TestSignOutKeepsTheDeviceSignedInWhenRevocationFails(t *testing.T) {
	resp := signOutWith(t, &fakeApp{signOut: errors.New("database unavailable")})
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Empty(t, clearedCookies(resp), "the session is still live, so the device keeps it to retry")
}

func TestSignOutOfATokenAccountDidNotIssueStillClearsTheDevice(t *testing.T) {
	resp := signOutWith(t, &fakeApp{signOut: application.ErrIdentityTokenInvalid})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.ElementsMatch(t, []string{AccessCookie, RefreshCookie}, clearedCookies(resp))
}

// Session cookies on the parent domain also end the host-only ones a browser
// kept from before, so the two are never sent side by side.
func TestSignOutOnTheParentDomainAlsoEndsHostOnlySessionCookies(t *testing.T) {
	resp := signOutWith(t, &fakeApp{}, "example.com")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	domains := map[string][]string{}
	for _, cookie := range resp.Cookies() {
		if cookie.MaxAge < 0 {
			domains[cookie.Name] = append(domains[cookie.Name], cookie.Domain)
		}
	}
	require.ElementsMatch(t, []string{"", "example.com"}, domains[AccessCookie])
	require.ElementsMatch(t, []string{"", "example.com"}, domains[RefreshCookie])
}
