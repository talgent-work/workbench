//go:build integration

package account_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/account/application"
	accounthttp "github.com/talgent-work/workbench/internal/business/account/transport/http"
)

func TestFirstSignInCreatesTheAccountAndSetsBothCookies(t *testing.T) {
	h := newHarness(t)
	h.github.set("first", githubUser{ID: 583231, Login: "octocat", Name: "", Emails: verifiedEmail("octocat@example.com")})
	d := h.device()

	resp := d.signIn("first")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/projects/alpha?tab=works", resp.Header.Get("Location"))
	require.NotEmpty(t, h.github.verifiers["first"], "the PKCE verifier reaches GitHub")

	accounts := h.accounts()
	require.Len(t, accounts, 1)
	require.Equal(t, "583231", accounts[0]["github_user_id"])
	require.Equal(t, "octocat@example.com", accounts[0]["email"])
	require.Equal(t, "octocat", accounts[0]["display_name"], "GitHub reported no name; the login stands in")

	cookies := map[string]*http.Cookie{}
	for _, cookie := range resp.Cookies() {
		cookies[cookie.Name] = cookie
	}
	access, refresh := cookies[accounthttp.AccessCookie], cookies[accounthttp.RefreshCookie]
	require.NotNil(t, access)
	require.NotNil(t, refresh)
	for _, cookie := range []*http.Cookie{access, refresh} {
		require.True(t, cookie.HttpOnly)
		require.True(t, cookie.Secure)
		require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	}
	require.Equal(t, "/", access.Path)
	require.Equal(t, "/auth/refresh", refresh.Path)

	token := h.verify(access.Value)
	subject, _ := token.Subject()
	require.Equal(t, accounts[0]["id"], subject)
	var principal, username, sid string
	require.NoError(t, token.Get("principal", &principal))
	require.NoError(t, token.Get("preferred_username", &username))
	require.NoError(t, token.Get("sid", &sid))
	require.Equal(t, "user", principal)
	require.Equal(t, "octocat", username)
	expires, _ := token.Expiration()
	require.WithinDuration(t, h.clock.Now().Add(15*time.Minute), expires, time.Second)
}

func TestReturningSignInRecordsItAndReplacesTheProfile(t *testing.T) {
	h := newHarness(t)
	h.github.set("first", githubUser{ID: 583231, Login: "octocat", Name: "Octo", Emails: verifiedEmail("octo@example.com")})
	h.device().signIn("first")
	h.clock.Advance(time.Hour)
	h.github.set("again", githubUser{ID: 583231, Login: "octo-renamed", Name: "Octo Cat", Emails: verifiedEmail("cat@example.com")})
	resp := h.device().signIn("again")
	require.Equal(t, http.StatusFound, resp.StatusCode)

	accounts := h.accounts()
	require.Len(t, accounts, 1)
	require.Equal(t, "octo-renamed", accounts[0]["username"])
	require.Equal(t, "Octo Cat", accounts[0]["display_name"])
	require.Equal(t, "cat@example.com", accounts[0]["email"])
}

func TestConcurrentFirstSignInsYieldOneAccount(t *testing.T) {
	h := newHarness(t)
	for i := range 6 {
		h.github.set(fmt.Sprintf("race-%d", i), githubUser{ID: 99, Login: "racer", Name: "Racer", Emails: verifiedEmail("racer@example.com")})
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := range 6 {
		wg.Go(func() {
			_, err := h.app.CompleteGitHubSignIn(context.Background(), application.CompleteGitHubSignIn{Code: fmt.Sprintf("race-%d", i), CodeVerifier: "v"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, h.accounts(), 1)
	sessions, err := h.engine.QueryString("SELECT id FROM login_session")
	require.NoError(t, err)
	require.Len(t, sessions, 6, "every sign-in opens its own session")
}

func TestFailedSignInsReturnToTheSignInPage(t *testing.T) {
	h := newHarness(t)
	h.github.set("noemail", githubUser{ID: 7, Login: "quiet", Emails: []map[string]any{{"email": "x@example.com", "primary": true, "verified": false}}})

	d := h.device()
	state := d.start("/projects/alpha")
	resp := d.callback(url.Values{"error": {"access_denied"}, "state": {state}}.Encode())
	require.Equal(t, "/login?error=denied&next=%2Fprojects%2Falpha", resp.Header.Get("Location"))

	state = d.start("/projects/alpha")
	resp = d.callback(url.Values{"code": {"first"}, "state": {"forged"}}.Encode())
	require.Equal(t, "/login?error=expired&next=%2Fprojects%2Falpha", resp.Header.Get("Location"), "state mismatch")
	_ = state

	resp = h.device().callback(url.Values{"code": {"first"}, "state": {"anything"}}.Encode())
	require.Equal(t, "/login?error=expired", resp.Header.Get("Location"), "opened in another browser")

	state = d.start("/")
	h.clock.Advance(11 * time.Minute)
	resp = d.callback(url.Values{"code": {"stale"}, "state": {state}}.Encode())
	require.Equal(t, "/login?error=expired", resp.Header.Get("Location"), "attempt older than ten minutes")

	state = d.start("/")
	resp = d.callback(url.Values{"code": {"stale"}, "state": {state}}.Encode())
	require.Equal(t, "/login?error=expired", resp.Header.Get("Location"), "GitHub rejected the code")

	state = d.start("/")
	resp = d.callback(url.Values{"code": {"noemail"}, "state": {state}}.Encode())
	require.Equal(t, "/login?error=email", resp.Header.Get("Location"))
	require.Empty(t, h.accounts())
}

func TestUserDirectoryIsPlatformWide(t *testing.T) {
	h := newHarness(t)
	for i, login := range []string{"Octocat", "octo-dog", "hubot", "cat-dog", "a-b"} {
		code := fmt.Sprintf("u%d", i)
		h.github.set(code, githubUser{ID: int64(i + 1), Login: login, Emails: verifiedEmail(login + "@example.com")})
		h.device().signIn(code)
	}
	found, err := h.app.SearchUsers(context.Background(), "OCTO", 10)
	require.NoError(t, err)
	require.Len(t, found, 2, "case-insensitive containment: Octocat, octo-dog")
	found, err = h.app.SearchUsers(context.Background(), "tocat", 10)
	require.NoError(t, err)
	require.Len(t, found, 1, "a fragment from the middle")
	found, err = h.app.SearchUsers(context.Background(), "o", 10)
	require.NoError(t, err)
	require.Empty(t, found, "one character is too short to search")
	found, err = h.app.SearchUsers(context.Background(), "o%", 10)
	require.NoError(t, err)
	require.Empty(t, found, "a wildcard in the term matches literally, not anything")
	found, err = h.app.SearchUsers(context.Background(), "to-do", 10)
	require.NoError(t, err)
	require.Len(t, found, 1, "a hyphenated fragment")
	require.Equal(t, "octo-dog", found[0].Username)
	id := found[0].ID
	found, err = h.app.SearchUsers(context.Background(), "o-d", 10)
	require.NoError(t, err)
	require.Len(t, found, 1, "single letters around a hyphen")
	require.Equal(t, "octo-dog", found[0].Username)
	for query, login := range map[string]string{"t-d": "cat-dog", "a-b": "a-b", "-b": "a-b"} {
		found, err = h.app.SearchUsers(context.Background(), query, 10)
		require.NoError(t, err)
		usernames := make([]string, 0, len(found))
		for _, user := range found {
			usernames = append(usernames, user.Username)
		}
		require.Contains(t, usernames, login, "single characters at the edges of hyphen segments: %s", query)
	}
	found, err = h.app.SearchUsers(context.Background(), "t-x", 10)
	require.NoError(t, err)
	require.Empty(t, found, "both letters present but not as one fragment")

	known, err := h.app.GetUsers(context.Background(), []string{id, "0190a0b2-0000-7000-8000-00000000dead", "garbage"})
	require.NoError(t, err)
	require.Len(t, known, 1)
	require.Equal(t, id, known[0].ID)
}
