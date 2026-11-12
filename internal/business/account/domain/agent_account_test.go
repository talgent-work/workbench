package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const agentLifetime = 7 * 24 * time.Hour

func workFacts(t *testing.T, represented string) WorkFacts {
	t.Helper()
	facts, err := NewWorkFacts("project-1", "agent-1", "case-work", "case", "case-1", represented, "")
	require.NoError(t, err)
	return facts
}

func TestAgentAccountIssuesAgentClaimsOfItsFacts(t *testing.T) {
	person, _ := NewAccountID()
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	account, err := OpenAgentAccount(work, workFacts(t, string(person)), now)
	require.NoError(t, err)

	claims, err := account.IssueIdentityToken(workFacts(t, string(person)), now, agentLifetime)
	require.NoError(t, err)
	require.Equal(t, PrincipalAgent, claims.Principal)
	require.Equal(t, string(work), claims.Subject)
	require.Equal(t, person, claims.Work.RepresentedPerson)
	require.Equal(t, now.Add(agentLifetime), claims.ExpiresAt)
	require.Empty(t, claims.SessionID)
}

func TestAgentAccountRefusesDifferentFacts(t *testing.T) {
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, workFacts(t, ""), time.Now())
	require.NoError(t, err)

	other, err := NewWorkFacts("project-2", "agent-1", "case-work", "case", "case-1", "", "")
	require.NoError(t, err)
	_, err = account.IssueIdentityToken(other, time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer)
}

func TestWorkFactsValidation(t *testing.T) {
	_, err := NewWorkFacts("", "", "purpose", "", "", "", "")
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "an AgentDefinition is required")
	_, err = NewWorkFacts("", "agent", "purpose", "case", "", "", "")
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "a Subject needs its id")
	_, err = NewWorkFacts("", "agent", "purpose", "", "", "not-an-account", "")
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	facts, err := NewWorkFacts("", "agent", "coordination", "", "", "", "")
	require.NoError(t, err)
	require.Empty(t, facts.RepresentedPerson, "an Agent representing no one has no person")
	_, err = OpenAgentAccount("not-a-uuid", facts, time.Now())
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	_, err = NewWorkFacts("", "agent", "work", "", "", "", "not-an-application")
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
}

// An Application's Work carries its owning Application among the facts every
// token for it states, and represents no person.
func TestWorkFactsCarryTheOwningApplication(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	facts, err := NewWorkFacts("", "agent", "work", "", "", "", " "+application+" ")
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), facts.OwningApplication)
	require.Empty(t, facts.RepresentedPerson)
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, facts, time.Now())
	require.NoError(t, err)
	claims, err := account.IssueIdentityToken(facts, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), claims.Work.OwningApplication)
	unowned, err := NewWorkFacts("", "agent", "work", "", "", "", "")
	require.NoError(t, err)
	_, err = account.IssueIdentityToken(unowned, time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, "the owning Application never drifts")
}
