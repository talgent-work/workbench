package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func profile(t *testing.T, login string) Profile {
	t.Helper()
	p, err := NewProfile(login, login+"@example.com", "Name "+login, "https://avatars.githubusercontent.com/u/1")
	require.NoError(t, err)
	return p
}

func TestAccountIDIsACanonicalUUID(t *testing.T) {
	id, err := NewAccountID()
	require.NoError(t, err)
	parsed, err := ParseAccountID(string(id))
	require.NoError(t, err)
	require.Equal(t, id, parsed)
	for _, bad := range []string{"", "374124958416109673", "0190A0B2-0000-7000-8000-000000000001", "not-a-uuid"} {
		_, err := ParseAccountID(bad)
		require.ErrorIs(t, err, ErrInvalidAccount, bad)
	}
}

func TestProfileRequiresGitHubFacts(t *testing.T) {
	_, err := NewProfile("", "a@example.com", "A", "")
	require.ErrorIs(t, err, ErrInvalidProfile)
	_, err = NewProfile("octocat", "", "Octo", "")
	require.ErrorIs(t, err, ErrInvalidProfile)
	_, err = NewProfile("octocat", "a@example.com", "Octo", "not a url")
	require.ErrorIs(t, err, ErrInvalidProfile)
	p, err := NewProfile(" octocat ", "a@example.com", "Octo", "")
	require.NoError(t, err)
	require.Equal(t, "octocat", p.Username)
}

func TestFirstSignInCreatesTheAccount(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	id, _ := NewAccountID()
	binding, err := NewGitHubBinding(583231)
	require.NoError(t, err)
	account, err := NewAccountFromGitHubSignIn(id, binding, profile(t, "octocat"), now)
	require.NoError(t, err)
	require.Equal(t, now, account.CreatedAt)
	require.Equal(t, now, account.LastSignedInAt)
	_, err = NewGitHubBinding(0)
	require.ErrorIs(t, err, ErrInvalidAccount)
}

func TestRecordSignInReplacesTheProfileOfTheBoundUser(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	id, _ := NewAccountID()
	account, err := NewAccountFromGitHubSignIn(id, 583231, profile(t, "octocat"), now)
	require.NoError(t, err)

	require.ErrorIs(t, account.RecordSignIn(1, profile(t, "intruder"), now.Add(time.Hour)), ErrGitHubBindingMismatch)
	require.Equal(t, "octocat", account.Profile.Username)

	renamed := profile(t, "octo-renamed")
	require.NoError(t, account.RecordSignIn(583231, renamed, now.Add(time.Hour)))
	require.Equal(t, renamed, account.Profile)
	require.Equal(t, now.Add(time.Hour), account.LastSignedInAt)
	require.Equal(t, now, account.CreatedAt)
}
