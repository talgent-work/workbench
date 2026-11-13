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

// projectWork supplies the facts of a Work started in the name of a project,
// found again by its case and reading and publishing within that case.
func projectWork(represented string) SuppliedWorkFacts {
	return SuppliedWorkFacts{
		WorkCallerKind: "project", WorkCallerID: "project-1",
		WorkKeyKind: "case", WorkKeyID: "case-1",
		FileHomeKind: "case", FileHomeID: "case-1",
		AgentID: "agent-1", Purpose: "case-work", RepresentedPerson: represented,
	}
}

// applicationWork supplies the facts of an Application's Standalone Work: the
// Application as its Work Caller and owner, and no Work Key or File Home.
func applicationWork(application string, labels map[string]string) SuppliedWorkFacts {
	return SuppliedWorkFacts{
		WorkCallerKind: "application", WorkCallerID: application,
		AgentID: "agent", Purpose: "work", OwningApplication: application, WorkLabels: labels,
	}
}

func admit(t *testing.T, supplied SuppliedWorkFacts) WorkFacts {
	t.Helper()
	facts, err := NewWorkFacts(supplied)
	require.NoError(t, err)
	return facts
}

func TestAgentAccountIssuesAgentClaimsOfItsFacts(t *testing.T) {
	person, _ := NewAccountID()
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	account, err := OpenAgentAccount(work, admit(t, projectWork(string(person))), now)
	require.NoError(t, err)

	claims, err := account.IssueIdentityToken(admit(t, projectWork(string(person))), now, agentLifetime)
	require.NoError(t, err)
	require.Equal(t, PrincipalAgent, claims.Principal)
	require.Equal(t, string(work), claims.Subject)
	require.Equal(t, WorkCaller{Kind: "project", ID: "project-1"}, claims.Work.WorkCaller)
	require.Equal(t, WorkKey{Kind: "case", ID: "case-1"}, claims.Work.WorkKey)
	require.Equal(t, FileHome{Kind: "case", ID: "case-1"}, claims.Work.FileHome)
	require.Equal(t, person, claims.Work.RepresentedPerson)
	require.Equal(t, now.Add(agentLifetime), claims.ExpiresAt)
	require.Empty(t, claims.SessionID)
}

func TestAgentAccountRefusesDifferentFacts(t *testing.T) {
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, admit(t, projectWork("")), time.Now())
	require.NoError(t, err)

	for name, drift := range map[string]func(*SuppliedWorkFacts){
		"another Work Caller": func(s *SuppliedWorkFacts) { s.WorkCallerID = "project-2" },
		"another Work Key":    func(s *SuppliedWorkFacts) { s.WorkKeyID = "case-2" },
		"no Work Key":         func(s *SuppliedWorkFacts) { s.WorkKeyKind, s.WorkKeyID = "", "" },
		"another File Home":   func(s *SuppliedWorkFacts) { s.FileHomeID = "case-2" },
		"no File Home":        func(s *SuppliedWorkFacts) { s.FileHomeKind, s.FileHomeID = "", "" },
	} {
		supplied := projectWork("")
		drift(&supplied)
		_, err = account.IssueIdentityToken(admit(t, supplied), time.Now(), agentLifetime)
		require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, name)
	}
}

func TestWorkFactsValidation(t *testing.T) {
	for name, invalid := range map[string]func(*SuppliedWorkFacts){
		"no AgentDefinition":    func(s *SuppliedWorkFacts) { s.AgentID = "" },
		"no Work Caller":        func(s *SuppliedWorkFacts) { s.WorkCallerKind, s.WorkCallerID = "", "" },
		"invalid person":        func(s *SuppliedWorkFacts) { s.RepresentedPerson = "not-an-account" },
		"invalid application":   func(s *SuppliedWorkFacts) { s.OwningApplication = "not-an-application" },
		"a Work Key without id": func(s *SuppliedWorkFacts) { s.WorkKeyID = "" },
		"a colon in a kind":     func(s *SuppliedWorkFacts) { s.FileHomeKind = "case:x" },
	} {
		supplied := projectWork("")
		invalid(&supplied)
		_, err := NewWorkFacts(supplied)
		require.ErrorIs(t, err, ErrInvalidAgentAccount, name)
	}

	supplied := projectWork("")
	supplied.WorkKeyKind, supplied.WorkKeyID, supplied.FileHomeKind, supplied.FileHomeID = "", "", "", ""
	facts := admit(t, supplied)
	require.Empty(t, facts.RepresentedPerson, "an Agent representing no one has no person")
	require.Zero(t, facts.WorkKey, "a Work without a Work Key has none")
	require.Zero(t, facts.FileHome, "a Work without a File Home has none")
	_, err := OpenAgentAccount("not-a-uuid", facts, time.Now())
	require.ErrorIs(t, err, ErrInvalidAgentAccount)
	_, err = OpenAgentAccount(WorkID(uuid.Must(uuid.NewV7()).String()), WorkFacts{AgentID: "agent", Purpose: "work"}, time.Now())
	require.ErrorIs(t, err, ErrInvalidAgentAccount, "an Agent Account never opens without a Work Caller")
}

// A Work Caller, Work Key or File Home is carried exactly as supplied or
// refused, for its real reason; Account interprets neither part.
func TestWorkReferencesAreCarriedExactlyOrRefused(t *testing.T) {
	caller, err := NewWorkCaller("project", "p:1")
	require.NoError(t, err)
	require.Equal(t, "project:p:1", caller.String())
	none, err := NewWorkKey("", "")
	require.NoError(t, err)
	require.Zero(t, none)
	require.Empty(t, none.String())

	for name, tc := range map[string]struct {
		build  func() error
		reason string
	}{
		"caller without kind":    {func() error { _, err := NewWorkCaller("", "p1"); return err }, "needs both"},
		"caller without id":      {func() error { _, err := NewWorkCaller("project", ""); return err }, "needs both"},
		"no caller":              {func() error { _, err := NewWorkCaller("", ""); return err }, "needs both"},
		"padded kind":            {func() error { _, err := NewWorkCaller(" project", "p1"); return err }, "surrounding whitespace"},
		"padded id":              {func() error { _, err := NewWorkCaller("project", "p1 "); return err }, "surrounding whitespace"},
		"blank id":               {func() error { _, err := NewWorkKey("case", " "); return err }, "surrounding whitespace"},
		"control character":      {func() error { _, err := NewWorkCaller("project", "p\n1"); return err }, "control character"},
		"kind with a colon":      {func() error { _, err := NewWorkCaller("pro:ject", "p1"); return err }, "holds ':'"},
		"kind too long":          {func() error { _, err := NewWorkCaller(strings.Repeat("k", 33), "p1"); return err }, "exceeds 32"},
		"id too long":            {func() error { _, err := NewWorkCaller("project", strings.Repeat("i", 256)); return err }, "exceeds 255"},
		"file home without kind": {func() error { _, err := NewFileHome("", "case-1"); return err }, "needs both"},
	} {
		err := tc.build()
		require.ErrorIs(t, err, ErrInvalidAgentAccount, name)
		require.ErrorContains(t, err, tc.reason, name)
	}
	_, err = NewWorkCaller(strings.Repeat("k", 32), strings.Repeat("i", 255))
	require.NoError(t, err, "a kind of 32 and an id of 255 characters fit")
}

// An Application's Work carries its owning Application among the facts every
// token for it states, and represents no person.
func TestWorkFactsCarryTheOwningApplication(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	supplied := applicationWork(application, nil)
	supplied.OwningApplication = " " + application + " "
	facts := admit(t, supplied)
	require.Equal(t, ApplicationID(application), facts.OwningApplication)
	require.Empty(t, facts.RepresentedPerson)
	work := WorkID(uuid.Must(uuid.NewV7()).String())
	account, err := OpenAgentAccount(work, facts, time.Now())
	require.NoError(t, err)
	claims, err := account.IssueIdentityToken(facts, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, ApplicationID(application), claims.Work.OwningApplication)
	unowned := applicationWork(application, nil)
	unowned.OwningApplication = ""
	_, err = account.IssueIdentityToken(admit(t, unowned), time.Now(), agentLifetime)
	require.ErrorIs(t, err, ErrAgentAccountFactsDiffer, "the owning Application never drifts")
}

// A Work's Work Labels are among the facts every token for it states, and a
// request stating other labels is refused.
func TestWorkFactsCarryTheWorkLabels(t *testing.T) {
	application := uuid.Must(uuid.NewV7()).String()
	labels := map[string]string{"user": "u123", "tenant": "acme"}
	facts := admit(t, applicationWork(application, labels))
	require.Equal(t, labels, facts.WorkLabels.Map())
	account, err := OpenAgentAccount(WorkID(uuid.Must(uuid.NewV7()).String()), facts, time.Now())
	require.NoError(t, err)
	same := admit(t, applicationWork(application, map[string]string{"tenant": "acme", "user": "u123"}))
	claims, err := account.IssueIdentityToken(same, time.Now(), agentLifetime)
	require.NoError(t, err)
	require.Equal(t, labels, claims.Work.WorkLabels.Map())
	other := admit(t, applicationWork(application, map[string]string{"user": "u456", "tenant": "acme"}))
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
		supplied := projectWork("")
		supplied.WorkLabels = labels
		_, err := NewWorkFacts(supplied)
		require.ErrorIs(t, err, ErrInvalidAgentAccount, name)
	}
}
