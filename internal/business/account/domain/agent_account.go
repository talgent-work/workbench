package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// WorkID is the Work an Agent Account belongs to: a canonical lowercase UUID,
// supplied by Agent.
type WorkID string

func ParseWorkID(raw string) (WorkID, error) {
	id := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", fmt.Errorf("%w: work id must be a canonical uuid", ErrInvalidAgentAccount)
	}
	return WorkID(id), nil
}

// WorkFacts are a Work's fixed facts as Agent supplies them. Account copies
// them into Identity Tokens and decides nothing from them (Facts carried, not
// read).
type WorkFacts struct {
	WorkCaller WorkCaller
	// WorkKey and FileHome are zero for a Work without one.
	WorkKey  WorkKey
	FileHome FileHome
	AgentID  string
	Purpose  string
	// RepresentedPerson is empty for an Agent that represents no one (No
	// person for an unrepresented Agent).
	RepresentedPerson AccountID
	// OwningApplication is the Application that owns the Work; empty for a
	// Work no Application owns.
	OwningApplication ApplicationID
	// WorkLabels are the Work's Work Labels; empty for none.
	WorkLabels WorkLabels
}

// maxFactLength bounds the AgentDefinition and purpose, as the columns that
// keep them.
const maxFactLength = 255

// SuppliedWorkFacts are a Work's fixed facts as Agent states them, before
// Account admits them.
type SuppliedWorkFacts struct {
	WorkCallerKind, WorkCallerID string
	WorkKeyKind, WorkKeyID       string
	FileHomeKind, FileHomeID     string
	AgentID, Purpose             string
	RepresentedPerson            string
	OwningApplication            string
	WorkLabels                   map[string]string
}

// NewWorkFacts admits the facts Agent supplies, once: the only way to build
// WorkFacts. A stored Agent Account is rehydrated through it as well.
func NewWorkFacts(supplied SuppliedWorkFacts) (WorkFacts, error) {
	caller, err := NewWorkCaller(supplied.WorkCallerKind, supplied.WorkCallerID)
	if err != nil {
		return WorkFacts{}, err
	}
	key, err := NewWorkKey(supplied.WorkKeyKind, supplied.WorkKeyID)
	if err != nil {
		return WorkFacts{}, err
	}
	home, err := NewFileHome(supplied.FileHomeKind, supplied.FileHomeID)
	if err != nil {
		return WorkFacts{}, err
	}
	facts := WorkFacts{
		WorkCaller: caller,
		WorkKey:    key,
		FileHome:   home,
		AgentID:    strings.TrimSpace(supplied.AgentID),
		Purpose:    strings.TrimSpace(supplied.Purpose),
	}
	if facts.AgentID == "" || facts.Purpose == "" {
		return WorkFacts{}, fmt.Errorf("%w: agent definition and purpose are required", ErrInvalidAgentAccount)
	}
	for _, fact := range []string{facts.AgentID, facts.Purpose} {
		if utf8.RuneCountInString(fact) > maxFactLength {
			return WorkFacts{}, fmt.Errorf("%w: a fact exceeds %d characters", ErrInvalidAgentAccount, maxFactLength)
		}
	}
	if represented := strings.TrimSpace(supplied.RepresentedPerson); represented != "" {
		id, err := ParseAccountID(represented)
		if err != nil {
			return WorkFacts{}, fmt.Errorf("%w: represented person must be an account id", ErrInvalidAgentAccount)
		}
		facts.RepresentedPerson = id
	}
	if owner := strings.TrimSpace(supplied.OwningApplication); owner != "" {
		id, err := ParseApplicationID(owner)
		if err != nil {
			return WorkFacts{}, fmt.Errorf("%w: owning application must be an application id", ErrInvalidAgentAccount)
		}
		facts.OwningApplication = id
	}
	if facts.WorkLabels, err = NewWorkLabels(supplied.WorkLabels); err != nil {
		return WorkFacts{}, err
	}
	return facts, nil
}

// AgentAccount is the identity of one Work's Agent, kept by Account as Agent
// directs. Its facts never change, and every token for the Work carries them.
type AgentAccount struct {
	WorkID    WorkID
	Facts     WorkFacts
	CreatedAt time.Time
	// Version is zero until the Agent Account is first saved.
	Version int
}

// OpenAgentAccount begins a Work's Agent Account with the facts of its first
// Identity Token request (Created with the first token).
func OpenAgentAccount(work WorkID, facts WorkFacts, now time.Time) (*AgentAccount, error) {
	if _, err := ParseWorkID(string(work)); err != nil {
		return nil, err
	}
	if facts.WorkCaller == (WorkCaller{}) {
		// WorkFacts come from NewWorkFacts; a zero value never opens one.
		return nil, fmt.Errorf("%w: work facts must be admitted by NewWorkFacts", ErrInvalidAgentAccount)
	}
	return &AgentAccount{WorkID: work, Facts: facts, CreatedAt: now.UTC()}, nil
}

// IssueIdentityToken states the Work's agent claims for lifetime, when the
// supplied facts equal the Agent Account's own (Facts never drift). The token
// is never refreshed: each call issues a new one.
func (a *AgentAccount) IssueIdentityToken(facts WorkFacts, now time.Time, lifetime time.Duration) (IdentityClaims, error) {
	if facts != a.Facts {
		return IdentityClaims{}, ErrAgentAccountFactsDiffer
	}
	if lifetime <= 0 {
		return IdentityClaims{}, fmt.Errorf("%w: agent token lifetime must be positive", ErrInvalidAgentAccount)
	}
	now = now.UTC()
	return IdentityClaims{
		Principal: PrincipalAgent,
		Subject:   string(a.WorkID),
		Work:      a.Facts,
		IssuedAt:  now,
		ExpiresAt: now.Add(lifetime),
	}, nil
}
