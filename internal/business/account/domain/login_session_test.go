package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var lifetimes = SessionLifetimes{Access: 15 * time.Minute, Sliding: 30 * 24 * time.Hour, Absolute: 90 * 24 * time.Hour}

func openSession(t *testing.T, now time.Time) (*LoginSession, RefreshSecret) {
	t.Helper()
	id, _ := NewAccountID()
	secret, err := NewRefreshSecret()
	require.NoError(t, err)
	session, err := OpenLoginSession(id, secret, "Mozilla/5.0", now, lifetimes)
	require.NoError(t, err)
	return session, secret
}

func current(s RefreshSecret) PresentedRefreshSecret {
	return PresentedRefreshSecret{Digest: s.Digest()}
}
func replaced(s RefreshSecret) PresentedRefreshSecret {
	return PresentedRefreshSecret{Digest: s.Digest(), Replaced: true}
}

func secret(t *testing.T) RefreshSecret {
	t.Helper()
	s, err := NewRefreshSecret()
	require.NoError(t, err)
	return s
}

func TestOpenIssuesTokensForItsAccount(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	session, issued := openSession(t, now)
	require.Equal(t, LoginSessionActive, session.State())
	require.Equal(t, issued.Digest(), session.RefreshSecret)
	require.NotEqual(t, string(issued), string(session.RefreshSecret), "only the digest is retained")
	require.Equal(t, now.Add(lifetimes.Sliding), session.ExpiresAt)
	require.Equal(t, now.Add(lifetimes.Absolute), session.AbsoluteExpiresAt)

	claims, err := session.IssueIdentityToken(profile(t, "octocat"), now, lifetimes)
	require.NoError(t, err)
	require.Equal(t, session.AccountReference, claims.Subject)
	require.Equal(t, PrincipalUser, claims.Principal)
	require.Equal(t, session.ID, claims.SessionID)
	require.Equal(t, now.Add(15*time.Minute), claims.ExpiresAt)
}

func TestRefreshReplacesTheSecretOnce(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	session, first := openSession(t, now)
	second := secret(t)

	later := now.Add(time.Hour)
	claims, err := session.Refresh(current(first), second, profile(t, "octocat"), later, lifetimes)
	require.NoError(t, err)
	require.Equal(t, later.Add(15*time.Minute), claims.ExpiresAt)
	require.Equal(t, second.Digest(), session.RefreshSecret)
	require.Equal(t, first.Digest(), session.ReplacedSecret)
	require.Equal(t, later.Add(lifetimes.Sliding), session.ExpiresAt)
	require.Equal(t, later, session.LastSeenAt)

	_, err = session.Refresh(current(secret(t)), secret(t), profile(t, "octocat"), later, lifetimes)
	require.ErrorIs(t, err, ErrRefreshSecretUnknown)
	require.Equal(t, LoginSessionActive, session.State(), "a secret never held has no effect")

	_, err = session.Refresh(PresentedRefreshSecret{Digest: secret(t).Digest(), Replaced: false}, secret(t), profile(t, "octocat"), later, lifetimes)
	require.ErrorIs(t, err, ErrRefreshSecretUnknown, "found under no session: no effect")

	_, err = session.Refresh(replaced(first), secret(t), profile(t, "octocat"), later, lifetimes)
	require.ErrorIs(t, err, ErrRefreshSecretReused)
	require.Equal(t, LoginSessionRevoked, session.State(), "reuse revokes the session")

	_, err = session.Refresh(current(second), secret(t), profile(t, "octocat"), later, lifetimes)
	require.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestSlidingExpiryNeverPassesTheAbsoluteLimit(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	session, held := openSession(t, now)
	at := now
	for at.Add(lifetimes.Sliding).Before(session.AbsoluteExpiresAt) {
		at = at.Add(lifetimes.Sliding - time.Hour)
		next := secret(t)
		_, err := session.Refresh(current(held), next, profile(t, "octocat"), at, lifetimes)
		require.NoError(t, err)
		held = next
	}
	require.Equal(t, session.AbsoluteExpiresAt, session.ExpiresAt)
	_, err := session.IssueIdentityToken(profile(t, "octocat"), session.AbsoluteExpiresAt, lifetimes)
	require.ErrorIs(t, err, ErrLoginSessionExpired)
}

func TestExpiredSessionsIssueNothing(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	session, held := openSession(t, now)
	_, err := session.Refresh(current(held), secret(t), profile(t, "octocat"), session.ExpiresAt, lifetimes)
	require.ErrorIs(t, err, ErrLoginSessionExpired)
	require.Equal(t, held.Digest(), session.RefreshSecret)
}

func TestRevokeIsTerminal(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	session, _ := openSession(t, now)
	other, _ := NewAccountID()
	require.False(t, session.Revoke(other, now), "another Account cannot end the session")
	require.Equal(t, LoginSessionActive, session.State())
	require.True(t, session.Revoke(session.AccountReference, now))
	require.False(t, session.Revoke(session.AccountReference, now.Add(time.Minute)))
	require.Equal(t, now, session.RevokedAt)
	_, err := session.IssueIdentityToken(profile(t, "octocat"), now, lifetimes)
	require.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestRefreshSecretFormIsChecked(t *testing.T) {
	_, err := ParseRefreshSecret("short")
	require.ErrorIs(t, err, ErrRefreshSecretUnknown)
	issued := secret(t)
	parsed, err := ParseRefreshSecret(string(issued))
	require.NoError(t, err)
	require.Equal(t, issued, parsed)
}
