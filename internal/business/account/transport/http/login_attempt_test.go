package http

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoginAttemptRoundTripsUntilItExpires(t *testing.T) {
	attempts := loginAttempts{key: []byte(strings.Repeat("k", 32)), ttl: 10 * time.Minute}
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	attempt, err := attempts.start("/projects/a", now)
	require.NoError(t, err)
	value, err := attempts.encode(attempt)
	require.NoError(t, err)

	decoded, err := attempts.decode(value, now.Add(9*time.Minute))
	require.NoError(t, err)
	require.Equal(t, attempt, decoded)
	require.NotEqual(t, decoded.State, decoded.Verifier)
	require.Len(t, codeChallenge(decoded.Verifier), 43)

	_, err = attempts.decode(value, now.Add(10*time.Minute))
	require.ErrorIs(t, err, errLoginAttemptInvalid, "older than ten minutes")
}

func TestLoginAttemptRejectsTampering(t *testing.T) {
	attempts := loginAttempts{key: []byte(strings.Repeat("k", 32)), ttl: 10 * time.Minute}
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	attempt, err := attempts.start("/", now)
	require.NoError(t, err)
	value, err := attempts.encode(attempt)
	require.NoError(t, err)

	other := loginAttempts{key: []byte(strings.Repeat("x", 32)), ttl: 10 * time.Minute}
	forged, err := other.encode(loginAttempt{State: attempt.State, Verifier: attempt.Verifier, Next: "//evil.example", ExpiresAt: attempt.ExpiresAt})
	require.NoError(t, err)

	body, signature, _ := strings.Cut(value, ".")
	for _, bad := range []string{"", "no-dot", body + ".", "." + signature, forged, body[:len(body)-2] + "AA." + signature} {
		_, err := attempts.decode(bad, now)
		require.ErrorIs(t, err, errLoginAttemptInvalid, bad)
	}
}

func TestInternalPathKeepsReturnsOnTheSite(t *testing.T) {
	for next, want := range map[string]string{
		"/projects/a?tab=b#c":                    "/projects/a?tab=b#c",
		"":                                       "/",
		"https://evil.example":                   "/",
		"//evil.example":                         "/",
		"/\\evil":                                "/",
		"javascript:alert(1)":                    "/",
		"/auth/logout":                           "/",
		"/login?next=/x":                         "/",
		"/login#x":                               "/",
		"/" + strings.Repeat("a", maxNextLength): "/",
	} {
		require.Equal(t, want, internalPath(next), next)
	}
}

// A sign-in returns to the entry it started on: a path asked on an entry's
// host stays on that entry, a page on another trusted entry is kept, and
// nothing reaches an untrusted origin or the sign-in endpoints. Failures show
// that entry's sign-in page.
func TestReturnTargetsReachOnlyTheEntryTheSignInStartedOn(t *testing.T) {
	targets, err := newReturnTargets("https://workbench.example.com/auth/github/callback", []string{"https://app.example.com/"})
	require.NoError(t, err)
	for _, tc := range []struct{ host, next, want string }{
		{"app.example.com", "/projects/a", "https://app.example.com/projects/a"},
		{"workbench.example.com", "/works/w1", "https://workbench.example.com/works/w1"},
		{"app.example.com", "https://workbench.example.com/works/w1?x=1", "https://workbench.example.com/works/w1?x=1"},
		{"app.example.com", "https://app.example.com", "https://app.example.com/"},
		{"app.example.com", "https://app.example.com/auth/logout", "https://app.example.com/"},
		{"app.example.com", "https://app.example.com//evil", "https://app.example.com/"},
		{"app.example.com", "https://user@app.example.com/", "https://app.example.com/"},
		{"app.example.com", "http://app.example.com/", "https://app.example.com/"},
		{"app.example.com", "https://evil.example/", "https://app.example.com/"},
		{"unknown.example", "/projects/a", "/projects/a"},
	} {
		require.Equal(t, tc.want, targets.from(tc.host, tc.next), tc.host+" "+tc.next)
	}
	require.Equal(t, "https://app.example.com/library", targets.resolve("https://app.example.com/library"))
	require.Equal(t, "/", targets.resolve("https://evil.example/"))

	page, path := targets.signInPage("https://app.example.com/library")
	require.Equal(t, "https://app.example.com/login", page)
	require.Equal(t, "/library", path)
	page, path = targets.signInPage("/")
	require.Equal(t, "/login", page, "a lost attempt fails on the callback host")
	require.Equal(t, "/", path)

	for _, bad := range []string{"app.example.com", "https://app.example.com/app", "ftp://x"} {
		_, err := newReturnTargets("", []string{bad})
		require.Error(t, err, bad)
	}
	_, err = newReturnTargets("not a url", nil)
	require.Error(t, err)
}
