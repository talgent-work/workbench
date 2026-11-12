package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const applicationLifetime = 15 * time.Minute

func registered(t *testing.T) (*Application, ClientSecretValue, time.Time) {
	t.Helper()
	owner, err := NewAccountID()
	require.NoError(t, err)
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	application, secret, err := RegisterApplication(owner, "  Support Desk ", now)
	require.NoError(t, err)
	return application, secret, now
}

func TestRegisterApplicationIssuesItsFirstSecretOnce(t *testing.T) {
	application, secret, now := registered(t)

	require.NoError(t, application.Validate())
	require.Equal(t, "Support Desk", application.Name)
	require.Equal(t, ApplicationActive, application.State)
	require.Len(t, application.Secrets, 1)
	kept := application.Secrets[0]
	require.Equal(t, ClientSecretCurrent, kept.State)
	require.True(t, strings.HasPrefix(string(secret), "wbs_"))
	require.NotContains(t, string(kept.SecretDigest), string(secret), "only the digest is kept")
	require.Equal(t, secret.Digest(), kept.SecretDigest)
	require.Equal(t, string(secret)[len(secret)-4:], kept.Hint)
	require.Equal(t, now, kept.IssuedAt)
	require.True(t, kept.LastUsedAt.IsZero())

	parsed, err := ParseClientSecretValue(string(secret))
	require.NoError(t, err)
	require.Equal(t, secret, parsed)
}

func TestRegisterApplicationRejectsABadNameOrOwner(t *testing.T) {
	owner, err := NewAccountID()
	require.NoError(t, err)
	for _, name := range []string{"", "   ", strings.Repeat("x", MaxApplicationName+1)} {
		_, _, err := RegisterApplication(owner, name, time.Now())
		require.ErrorIs(t, err, ErrInvalidApplication, name)
	}
	_, _, err = RegisterApplication("not-an-account", "Desk", time.Now())
	require.ErrorIs(t, err, ErrInvalidApplication)
	_, _, err = RegisterApplication(owner, strings.Repeat("名", MaxApplicationName), time.Now())
	require.NoError(t, err, "the limit counts characters, not bytes")
}

func TestApplicationIssuesApplicationClaimsForItsSecretAndRecordsUse(t *testing.T) {
	application, secret, now := registered(t)
	later := now.Add(time.Hour)

	claims, recorded, err := application.IssueIdentityToken(secret, later, applicationLifetime)
	require.NoError(t, err)
	require.True(t, recorded)
	require.Equal(t, PrincipalApplication, claims.Principal)
	require.Equal(t, string(application.ID), claims.Subject)
	require.Equal(t, later.Add(applicationLifetime), claims.ExpiresAt)
	require.Empty(t, claims.SessionID)
	require.Equal(t, WorkFacts{}, claims.Work)
	require.Equal(t, later, application.Secrets[0].LastUsedAt)

	_, recorded, err = application.IssueIdentityToken(secret, later.Add(LastUseResolution-time.Second), applicationLifetime)
	require.NoError(t, err)
	require.False(t, recorded, "a use within the resolution writes nothing")
	require.Equal(t, later, application.Secrets[0].LastUsedAt)
	_, recorded, err = application.IssueIdentityToken(secret, later.Add(LastUseResolution), applicationLifetime)
	require.NoError(t, err)
	require.True(t, recorded)
}

func TestApplicationRefusesAnotherSecret(t *testing.T) {
	application, _, now := registered(t)
	_, otherSecret, _ := registered(t)

	_, _, err := application.IssueIdentityToken(otherSecret, now, applicationLifetime)
	require.ErrorIs(t, err, ErrClientSecretMismatch)
	require.True(t, application.Secrets[0].LastUsedAt.IsZero())
}

func TestApplicationIssuesNothingOnceRemoved(t *testing.T) {
	application, secret, now := registered(t)
	application.State = ApplicationRemoved
	application.Secrets[0].State = ClientSecretEnded

	_, _, err := application.IssueIdentityToken(secret, now, applicationLifetime)
	require.ErrorIs(t, err, ErrClientSecretMismatch)
}

func TestClientSecretMatchesInItsOverlapOnly(t *testing.T) {
	application, secret, now := registered(t)
	kept := application.Secrets[0]
	kept.State = ClientSecretOverlapping
	kept.OverlapEndsAt = now.Add(24 * time.Hour)

	matched, _ := kept.Match(secret.Digest(), now.Add(23*time.Hour))
	require.True(t, matched)
	matched, _ = kept.Match(secret.Digest(), now.Add(24*time.Hour))
	require.False(t, matched)
	kept.State = ClientSecretEnded
	matched, _ = kept.Match(secret.Digest(), now)
	require.False(t, matched)
}

func TestValidSecretsListTheCurrentFirst(t *testing.T) {
	application, _, now := registered(t)
	next, _, err := IssueClientSecret(now)
	require.NoError(t, err)
	application.Secrets[0].State = ClientSecretOverlapping
	application.Secrets[0].OverlapEndsAt = now.Add(time.Hour)
	ended, _, err := IssueClientSecret(now)
	require.NoError(t, err)
	ended.State = ClientSecretEnded
	application.Secrets = append(application.Secrets, ended, next)
	require.NoError(t, application.Validate())

	valid := application.ValidSecrets(now)
	require.Equal(t, []*ClientSecret{next, application.Secrets[0]}, valid)
	require.Equal(t, []*ClientSecret{next}, application.ValidSecrets(now.Add(time.Hour)))
}

func TestApplicationHoldsAtMostOneCurrentSecret(t *testing.T) {
	application, _, now := registered(t)
	second, _, err := IssueClientSecret(now)
	require.NoError(t, err)
	application.Secrets = append(application.Secrets, second)
	require.ErrorIs(t, application.Validate(), ErrInvalidApplication)
}

func TestParseClientSecretValueAcceptsOnlyTheIssuedForm(t *testing.T) {
	for _, raw := range []string{"", "wbs_", "wbs_short", "abc_" + strings.Repeat("A", 43), "wbs_" + strings.Repeat("A", 42) + "\n"} {
		_, err := ParseClientSecretValue(raw)
		require.ErrorIs(t, err, ErrClientSecretMismatch, raw)
	}
}

func TestOwnedByIsTheApplicationOwnerOnly(t *testing.T) {
	application, _, _ := registered(t)
	stranger, err := NewAccountID()
	require.NoError(t, err)
	require.True(t, application.OwnedBy(application.ApplicationOwner))
	require.False(t, application.OwnedBy(stranger))
	require.False(t, application.OwnedBy(""))
}

const rotationOverlap = 24 * time.Hour

func grantSucceeds(t *testing.T, application *Application, secret ClientSecretValue, at time.Time) bool {
	t.Helper()
	_, _, err := application.IssueIdentityToken(secret, at, applicationLifetime)
	if err != nil {
		require.ErrorIs(t, err, ErrClientSecretMismatch)
		return false
	}
	return true
}

func TestRotateSecretKeepsTheReplacedSecretForTheOverlap(t *testing.T) {
	application, first, now := registered(t)

	second, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NoError(t, application.Validate())

	valid := application.ValidSecrets(now)
	require.Len(t, valid, 2)
	require.Equal(t, ClientSecretCurrent, valid[0].State)
	require.Equal(t, second.Digest(), valid[0].SecretDigest)
	require.Equal(t, ClientSecretOverlapping, valid[1].State)
	require.Equal(t, now.Add(rotationOverlap), valid[1].OverlapEndsAt)

	require.True(t, grantSucceeds(t, application, first, now.Add(rotationOverlap-time.Second)))
	require.True(t, grantSucceeds(t, application, second, now.Add(rotationOverlap-time.Second)))
	require.False(t, grantSucceeds(t, application, first, now.Add(rotationOverlap)), "the overlap has ended")
	require.True(t, grantSucceeds(t, application, second, now.Add(rotationOverlap)))
}

func TestRotatingDuringAnOverlapEndsTheEarlierOverlapAtOnce(t *testing.T) {
	application, first, now := registered(t)
	second, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.NoError(t, err)

	third, err := application.RotateSecret(application.ApplicationOwner, now.Add(time.Hour), rotationOverlap)
	require.NoError(t, err)
	require.NoError(t, application.Validate())

	at := now.Add(2 * time.Hour)
	require.False(t, grantSucceeds(t, application, first, at))
	require.True(t, grantSucceeds(t, application, second, at))
	require.True(t, grantSucceeds(t, application, third, at))
	require.Len(t, application.ValidSecrets(at), 2)
}

func TestRevokeSecretRefusesItAtOnce(t *testing.T) {
	application, first, now := registered(t)
	second, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.NoError(t, err)
	overlapping := application.ValidSecrets(now)[1]

	require.NoError(t, application.RevokeSecret(application.ApplicationOwner, overlapping.ID, now))
	require.False(t, grantSucceeds(t, application, first, now))
	require.True(t, grantSucceeds(t, application, second, now))
	require.ErrorIs(t, application.RevokeSecret(application.ApplicationOwner, overlapping.ID, now), ErrClientSecretNotFound, "an ended secret is not revoked twice")

	current := application.ValidSecrets(now)[0]
	require.NoError(t, application.RevokeSecret(application.ApplicationOwner, current.ID, now))
	require.False(t, grantSucceeds(t, application, second, now))
	require.Empty(t, application.ValidSecrets(now))
	require.NoError(t, application.Validate())

	third, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.NoError(t, err)
	require.True(t, grantSucceeds(t, application, third, now), "rotation restores a secret after the last was revoked")
	require.Len(t, application.ValidSecrets(now), 1)
}

func TestRevokeSecretRefusesASecretWhoseOverlapEnded(t *testing.T) {
	application, _, now := registered(t)
	_, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.NoError(t, err)
	overlapping := application.ValidSecrets(now)[1]

	require.ErrorIs(t, application.RevokeSecret(application.ApplicationOwner, overlapping.ID, now.Add(rotationOverlap)), ErrClientSecretNotFound)
	require.ErrorIs(t, application.RevokeSecret(application.ApplicationOwner, "unknown", now), ErrClientSecretNotFound)
}

func TestARemovedApplicationNeitherRotatesNorRevokes(t *testing.T) {
	application, _, now := registered(t)
	id := application.Secrets[0].ID
	application.State = ApplicationRemoved
	application.Secrets[0].End()

	_, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.ErrorIs(t, err, ErrApplicationNotFound)
	require.ErrorIs(t, application.RevokeSecret(application.ApplicationOwner, id, now), ErrApplicationNotFound)
}

func TestOnlyTheOwnerRotatesOrRevokes(t *testing.T) {
	application, secret, now := registered(t)
	stranger, err := NewAccountID()
	require.NoError(t, err)

	_, err = application.RotateSecret(stranger, now, rotationOverlap)
	require.ErrorIs(t, err, ErrApplicationNotFound)
	require.ErrorIs(t, application.RevokeSecret(stranger, application.Secrets[0].ID, now), ErrApplicationNotFound)
	require.Len(t, application.Secrets, 1)
	require.True(t, grantSucceeds(t, application, secret, now), "the refused attempts changed nothing")
}

func TestRotateSecretRefusesANonPositiveOverlapAsAFault(t *testing.T) {
	application, _, now := registered(t)
	_, err := application.RotateSecret(application.ApplicationOwner, now, 0)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidApplication, "a misconfigured overlap is not the caller's fault")
	require.Len(t, application.Secrets, 1)
}

func TestRemoveEndsEverySecretAndIssuesNothingAgain(t *testing.T) {
	application, first, now := registered(t)
	second, err := application.RotateSecret(application.ApplicationOwner, now, rotationOverlap)
	require.NoError(t, err)

	removed, err := application.Remove(application.ApplicationOwner, now.Add(time.Minute))
	require.NoError(t, err)

	require.Equal(t, ApplicationRemovedEvent{ApplicationID: application.ID, At: now.Add(time.Minute)}, removed)
	require.Equal(t, ApplicationRemoved, application.State)
	require.NoError(t, application.Validate(), "a removed Application holds no valid secret")
	require.Empty(t, application.ValidSecrets(now.Add(time.Minute)))
	for _, secret := range []ClientSecretValue{first, second} {
		require.False(t, grantSucceeds(t, application, secret, now.Add(time.Minute)), "neither the current nor the overlapping secret matches")
	}
}

func TestOnlyTheOwnerRemovesAndOnlyOnce(t *testing.T) {
	application, secret, now := registered(t)
	stranger, err := NewAccountID()
	require.NoError(t, err)

	_, err = application.Remove(stranger, now)
	require.ErrorIs(t, err, ErrApplicationNotFound)
	require.Equal(t, ApplicationActive, application.State)
	require.True(t, grantSucceeds(t, application, secret, now), "the refused removal changed nothing")

	_, err = application.Remove(application.ApplicationOwner, now)
	require.NoError(t, err)
	_, err = application.Remove(application.ApplicationOwner, now)
	require.ErrorIs(t, err, ErrApplicationNotFound, "a removed Application never returns")
}
