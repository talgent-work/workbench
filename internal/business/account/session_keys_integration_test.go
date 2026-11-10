//go:build integration

package account_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/stretchr/testify/require"

	accounthttp "github.com/talgent-work/workbench/internal/business/account/transport/http"
)

func TestRefreshRotatesAndReuseRevokesTheSession(t *testing.T) {
	h := newHarness(t)
	h.github.set("first", githubUser{ID: 1, Login: "octocat", Emails: verifiedEmail("o@example.com")})
	d := h.device()
	d.signIn("first")
	firstSecret := d.cookie(accounthttp.RefreshCookie, "/auth/refresh")
	firstAccess := d.cookie(accounthttp.AccessCookie, "/")

	require.Equal(t, http.StatusUnsupportedMediaType, d.post("/auth/refresh", "application/x-www-form-urlencoded").StatusCode)

	h.clock.Advance(20 * time.Minute)
	resp := d.post("/auth/refresh", "application/json")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	secondSecret := d.cookie(accounthttp.RefreshCookie, "/auth/refresh")
	secondAccess := d.cookie(accounthttp.AccessCookie, "/")
	require.NotEqual(t, firstSecret, secondSecret)
	require.NotEqual(t, firstAccess, secondAccess)
	h.verify(secondAccess)

	// Someone presents the replaced secret: the session is revoked.
	stolen := h.device()
	stolen.client.Jar.SetCookies(mustURL(h.server.URL+"/auth/refresh"), []*http.Cookie{{Name: accounthttp.RefreshCookie, Value: firstSecret, Path: "/auth/refresh", Secure: true}})
	require.Equal(t, http.StatusUnauthorized, stolen.post("/auth/refresh", "application/json").StatusCode)

	resp = d.post("/auth/refresh", "application/json")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the device signs in again")
	require.Empty(t, d.cookie(accounthttp.RefreshCookie, "/auth/refresh"), "cookies are cleared")

	// A secret the session never held changes nothing.
	other := h.device()
	other.signIn("first")
	never := h.device()
	never.client.Jar.SetCookies(mustURL(h.server.URL+"/auth/refresh"), []*http.Cookie{{Name: accounthttp.RefreshCookie, Value: strings.Repeat("A", 43), Path: "/auth/refresh", Secure: true}})
	require.Equal(t, http.StatusUnauthorized, never.post("/auth/refresh", "application/json").StatusCode)
	require.Equal(t, http.StatusNoContent, other.post("/auth/refresh", "application/json").StatusCode)
}

func TestLogoutEndsOnlyThisDevice(t *testing.T) {
	h := newHarness(t)
	h.github.set("first", githubUser{ID: 1, Login: "octocat", Emails: verifiedEmail("o@example.com")})
	laptop, phone := h.device(), h.device()
	laptop.signIn("first")
	phone.signIn("first")

	// Signing out works after the token has expired too.
	h.clock.Advance(time.Hour)
	resp := laptop.post("/auth/logout", "application/json")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Empty(t, laptop.cookie(accounthttp.AccessCookie, "/"))
	revoked, err := h.engine.QueryString("SELECT id FROM login_session WHERE revoked_at > 0")
	require.NoError(t, err)
	require.Len(t, revoked, 1)

	require.Equal(t, http.StatusUnauthorized, laptop.post("/auth/refresh", "application/json").StatusCode)
	require.Equal(t, http.StatusNoContent, phone.post("/auth/refresh", "application/json").StatusCode, "the other device stays signed in")
}

func TestRetiringKeysStayPublishedUntilTheirTokensExpire(t *testing.T) {
	h := newHarness(t)
	h.github.set("first", githubUser{ID: 1, Login: "octocat", Emails: verifiedEmail("o@example.com")})
	ctx := context.Background()

	// Near the end of the first key's interval its successor is published
	// one overlap ahead of signing.
	h.clock.Advance(30*24*time.Hour - 10*time.Minute)
	require.NoError(t, h.app.RotateSigningKeys(ctx))
	published := func() int {
		body, err := h.app.PublishedKeys(ctx)
		require.NoError(t, err)
		set, err := jwk.Parse(body)
		require.NoError(t, err)
		return set.Len()
	}
	require.Equal(t, 2, published())

	d := h.device()
	d.signIn("first")
	oldKeyToken := d.cookie(accounthttp.AccessCookie, "/")
	agentToken := h.issueAgentToken(newWorkID(), "")

	h.clock.Advance(11 * time.Minute)
	h.verify(oldKeyToken)
	require.Equal(t, 2, published(), "the retiring key stays while its tokens may be valid")

	// The successor signs now, and its tokens verify.
	require.Equal(t, http.StatusNoContent, d.post("/auth/refresh", "application/json").StatusCode)
	h.verify(d.cookie(accounthttp.AccessCookie, "/"))

	// A person's token is long gone, but an agent's from the old key is
	// still valid: the key stays published for the longest token lifetime.
	h.clock.Advance(agentTokenLifetime - 12*time.Minute)
	require.NoError(t, h.app.RotateSigningKeys(ctx))
	require.Equal(t, 2, published())
	h.verify(agentToken)

	h.clock.Advance(12 * time.Minute)
	require.NoError(t, h.app.RotateSigningKeys(ctx))
	require.Equal(t, 1, published(), "the retiring key goes once its last token expired")
}

func TestConcurrentRotationProvisionsOneSuccessor(t *testing.T) {
	h := newHarness(t)
	h.clock.Advance(30 * 24 * time.Hour)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { require.NoError(t, h.app.RotateSigningKeys(context.Background())) })
	}
	wg.Wait()
	rows, err := h.engine.QueryString("SELECT generation FROM signing_key WHERE deleted_at = 0 ORDER BY generation")
	require.NoError(t, err)
	require.Len(t, rows, 2)
}
