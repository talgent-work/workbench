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

var projectCaller = WorkCaller{Kind: "project", ID: "project-1"}

func workFacts(t *testing.T, represented string) WorkFacts {
	t.Helper()
	facts, err := NewWorkFacts(projectCaller, WorkKey{Kind: "case", ID: "case-1"}, FileHome{Kind: "case", ID: "case-1"}, "agent-1", "case-work", represented, "", nil)
	require.NoError(t, err)
	return facts
}

// applicationFacts are the facts of an Application's Standalone Work: the
// Application as its Work Caller and owner, and no Work Key or File Home.
func applicationFacts(t *testing.T, application string, labels map[string]string) WorkFacts {
	t.Helper()
	facts, err := NewWorkFacts(WorkCaller{Kind: "application", ID: application}, WorkKey{}, FileHome{}, "agent", "work", "", application, labels)
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
	require.Equal(t, workFacts(t, string(person)), claims.Work, "the claims carry every fact of the Work")
	require.Equal(t, person, claims.Work.RepresentedPerson)
	require.Equal(t, now.Add(agentLifetime), claims.ExpiresAt)
	require.Empty(t, claims.SessionID)
}

func TestAgentAccountRefusesDifferentFacts(t *testing.T) {
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, workFacts(t, ""), time.Now())
	require.NoError(t, err)

	key := WorkKey{Kind: "case", ID: "case-1"}
	home := FileHome{Kind: "case", ID: "case-1"}
	for name, other := range map[string]struct {
		caller WorkCaller
		key    WorkKey
		home   FileHome
	}{
		"another Work Caller": {WorkCaller{Kind: "project", ID: "project-2"}, key, home},
		"another Work Key":    {projectCaller, WorkKey{Kind: "case", ID: "case-2"}, home},
		"no Work Key":         {projectCaller, WorkKey{}, home},
		"another File Home":   {projectCaller, key, FileHome{Kind: "case", ID: "case-2"}},
		"no File Home":        {projectCaller, key, FileHome{}},
	} {
		facts, err := NewWorkFacts(other.caller, other.key, other.home, "agent-1", "case-work", "", "", nil)
		require.NoError(t, err)
		_, err = account.IssueIdentityToken(facts, time.Now(), agentLifetime)
		require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, name)
	}
}

func TestWorkFactsValidation(t *testing.T) {
	_, err := NewWorkFacts(projectCaller, WorkKey{}, FileHome{}, "", "purpose", "", "", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "an AgentDefinition is required")
	_, err = NewWorkFacts(WorkCaller{}, WorkKey{}, FileHome{}, "agent", "purpose", "", "", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "a Work Caller is required")
	_, err = NewWorkFacts(projectCaller, WorkKey{}, FileHome{}, "agent", "purpose", "not-an-account", "", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	facts, err := NewWorkFacts(projectCaller, WorkKey{}, FileHome{}, "agent", "coordination", "", "", nil)
	require.NoError(t, err)
	require.Empty(t, facts.RepresentedPerson, "an Agent representing no one has no person")
	require.Zero(t, facts.WorkKey, "a Work without a Work Key has none")
	require.Zero(t, facts.FileHome, "a Work without a File Home has none")
	_, err = OpenAgentAccount("not-a-uuid", facts, time.Now())
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	_, err = OpenAgentAccount(WorkID(uuid.Must(uuid.NewV7()).String()), WorkFacts{AgentID: "agent", Purpose: "work"}, time.Now())
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "an Agent Account never opens without a Work Caller")
	_, err = NewWorkFacts(projectCaller, WorkKey{}, FileHome{}, "agent", "work", "", "not-an-application", nil)
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
}

// A Work Caller, Work Key or File Home is a kind and an identity, each bounded
// so that "<kind>:<id>" splits at its first colon; Account interprets neither.
func TestWorkReferencesAreBounded(t *testing.T) {
	caller, err := NewWorkCaller(" project ", " p:1 ")
	require.NoError(t, err)
	require.Equal(t, WorkCaller{Kind: "project", ID: "p:1"}, caller)
	require.Equal(t, "project:p:1", caller.String())
	none, err := NewWorkKey(" ", "")
	require.NoError(t, err)
	require.Zero(t, none)
	require.Empty(t, none.String())

	for name, build := range map[string]func() error{
		"caller without kind":    func() error { _, err := NewWorkCaller("", "p1"); return err },
		"caller without id":      func() error { _, err := NewWorkCaller("project", " "); return err },
		"no caller":              func() error { _, err := NewWorkCaller("", ""); return err },
		"kind with a colon":      func() error { _, err := NewWorkCaller("pro:ject", "p1"); return err },
		"kind too long":          func() error { _, err := NewWorkCaller(strings.Repeat("k", 33), "p1"); return err },
		"id too long":            func() error { _, err := NewWorkCaller("project", strings.Repeat("i", 256)); return err },
		"control character":      func() error { _, err := NewWorkCaller("project", "p\n1"); return err },
		"work key without id":    func() error { _, err := NewWorkKey("case", ""); return err },
		"file home without kind": func() error { _, err := NewFileHome("", "case-1"); return err },
	} {
		require.ErrorIs(t, build(), ErrInvalidAgentAccount, name)
	}
	_, err = NewWorkCaller(strings.Repeat("k", 32), strings.Repeat("i", 255))
	require.NoError(t, err, "a kind of 32 and an id of 255 characters fit")
}

// An Application's Work carries its owning Application among the facts every
// token for it states, and represents no person.
func TestWorkFactsCarryTheOwningApplication(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	facts, err := NewWorkFacts(WorkCaller{Kind: "application", ID: application}, WorkKey{}, FileHome{}, "agent", "work", "", " "+application+" ", nil)
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), facts.OwningApplication)
	require.Empty(t, facts.RepresentedPerson)
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, facts, time.Now())
	require.NoError(t, err)
	claims, err := account.IssueIdentityToken(facts, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), claims.Work.OwningApplication)
	unowned, err := NewWorkFacts(WorkCaller{Kind: "application", ID: application}, WorkKey{}, FileHome{}, "agent", "work", "", "", nil)
	require.NoError(t, err)
	_, err = account.IssueIdentityToken(unowned, time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, "the owning Application never drifts")
}

// A Work's Work Labels are among the facts every token for it states, and a
// request stating other labels is refused.
func TestWorkFactsCarryTheWorkLabels(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	labels := map[string]string{"user": "u123", "tenant": "acme"}
	facts := applicationFacts(t, application, labels)
	require.Equal(t, labels, facts.WorkLabels.Map())
	account, err := OpenAgentAccount(WorkID(uuid.Must(uuid.NewV7()).String()), facts, time.Now())
	require.NoError(t, err)
	same := applicationFacts(t, application, map[string]string{"tenant": "acme", "user": "u123"})
	claims, err := account.IssueIdentityToken(same, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, labels, claims.Work.WorkLabels.Map())
	other := applicationFacts(t, application, map[string]string{"user": "u456", "tenant": "acme"})
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
		_, err := NewWorkFacts(projectCaller, WorkKey{}, FileHome{}, "agent", "work", "", "", labels)
		require.ErrorIs(t, err, ErrInvalidAgentAccount, name)
	}
}
