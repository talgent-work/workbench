package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const agentLifetime = 7 * 24 * time.Hour

func workFacts(t *testing.T, represented string) WorkFacts {
	t.Helper()
	facts, err := NewWorkFacts("project-1", "agent-1", "case-work", "case", "case-1", represented, "", nil)
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

	other, err := NewWorkFacts("project-2", "agent-1", "case-work", "case", "case-1", "", "", nil)
	require.NoError(t, err)
	_, err = account.IssueIdentityToken(other, time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer)
}

func TestWorkFactsValidation(t *testing.T) {
	_, err := NewWorkFacts("", "", "purpose", "", "", "", "", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "an AgentDefinition is required")
	_, err = NewWorkFacts("", "agent", "purpose", "case", "", "", "", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "a Subject needs its id")
	_, err = NewWorkFacts("", "agent", "purpose", "", "", "not-an-account", "", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	facts, err := NewWorkFacts("", "agent", "coordination", "", "", "", "", nil)
	require.NoError(t, err)
	require.Empty(t, facts.RepresentedPerson, "an Agent representing no one has no person")
	_, err = OpenAgentAccount("not-a-uuid", facts, time.Now())
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	_, err = NewWorkFacts("", "agent", "work", "", "", "", "not-an-application", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
}

// An Application's Work carries its owning Application among the facts every
// token for it states, and represents no person.
func TestWorkFactsCarryTheOwningApplication(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	facts, err := NewWorkFacts("", "agent", "work", "", "", "", " "+application+" ", nil)
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), facts.OwningApplication)
	require.Empty(t, facts.RepresentedPerson)
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, facts, time.Now())
	require.NoError(t, err)
	claims, err := account.IssueIdentityToken(facts, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), claims.Work.OwningApplication)
	unowned, err := NewWorkFacts("", "agent", "work", "", "", "", "", nil)
	require.NoError(t, err)
	_, err = account.IssueIdentityToken(unowned, time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, "the owning Application never drifts")
}

// A Work's Work Labels are among the facts every token for it states, and a
// request stating other labels is refused.
func TestWorkFactsCarryTheWorkLabels(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	labels := map[string]string{"user": "u123", "tenant": "acme"}
	facts, err := NewWorkFacts("", "agent", "work", "", "", "", application, labels)
	require.NoError(t, err)
	require.Equal(t, labels, facts.WorkLabels.Map())
	account, err := OpenAgentAccount(WorkID(uuid.Must(uuid.NewV7()).String()), facts, time.Now())
	require.NoError(t, err)
	same, err := NewWorkFacts("", "agent", "work", "", "", "", application, map[string]string{"tenant": "acme", "user": "u123"})
	require.NoError(t, err)
	claims, err := account.IssueIdentityToken(same, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, labels, claims.Work.WorkLabels.Map())
	other, err := NewWorkFacts("", "agent", "work", "", "", "", application, map[string]string{"user": "u456", "tenant": "acme"})
	require.NoError(t, err)
	_, err = account.IssueIdentityToken(other, time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, "the Work Labels never drift")
}

func TestWorkFactsBoundTheWorkLabels(t *testing.T) {
	tooMany := make(map[string]string, 17)
	for i := range 17 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	for name, labels := range map[string]map[string]string{
		"too many":    tooMany,
		"empty key":   {"": "v"},
		"empty value": {"k": ""},
		"long key":    {strings.Repeat("k", 65): "v"},
		"long value":  {"k": strings.Repeat("v", 257)},
	} {
		_, err := NewWorkFacts("", "agent", "work", "", "", "", "", labels)
		require.ErrorIs(t, err, ErrInvalidAgentAccount, name)
	}
}
