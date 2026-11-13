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

// NewWorkFacts trims and validates the facts Agent supplies.
func NewWorkFacts(caller WorkCaller, key WorkKey, home FileHome, agentID, purpose, representedPerson, owningApplication string, workLabels map[string]string) (WorkFacts, error) {
	facts := WorkFacts{
		WorkCaller: caller,
		WorkKey:    key,
		FileHome:   home,
		AgentID:    strings.TrimSpace(agentID),
		Purpose:    strings.TrimSpace(purpose),
	}
	if represented := strings.TrimSpace(representedPerson); represented != "" {
		id, err := ParseAccountID(represented)
		if err != nil {
			return WorkFacts{}, fmt.Errorf("%w: represented person must be an account id", ErrInvalidAgentAccount)
		}
		facts.RepresentedPerson = id
	}
	if owner := strings.TrimSpace(owningApplication); owner != "" {
		id, err := ParseApplicationID(owner)
		if err != nil {
			return WorkFacts{}, fmt.Errorf("%w: owning application must be an application id", ErrInvalidAgentAccount)
		}
		facts.OwningApplication = id
	}
	labels, err := NewWorkLabels(workLabels)
	if err != nil {
		return WorkFacts{}, err
	}
	facts.WorkLabels = labels
	if err := facts.Validate(); err != nil {
		return WorkFacts{}, err
	}
	return facts, nil
}

// Validate requires a Work Caller, an AgentDefinition and a purpose, and that
// each Work Caller, Work Key and File Home is one its constructor admits.
func (f WorkFacts) Validate() error {
	if f.AgentID == "" || f.Purpose == "" {
		return fmt.Errorf("%w: agent definition and purpose are required", ErrInvalidAgentAccount)
	}
	for _, fact := range []string{f.AgentID, f.Purpose} {
		if utf8.RuneCountInString(fact) > maxFactLength {
			return fmt.Errorf("%w: a fact exceeds %d characters", ErrInvalidAgentAccount, maxFactLength)
		}
	}
	caller, err := NewWorkCaller(f.WorkCaller.Kind, f.WorkCaller.ID)
	if err != nil {
		return err
	}
	key, err := NewWorkKey(f.WorkKey.Kind, f.WorkKey.ID)
	if err != nil {
		return err
	}
	home, err := NewFileHome(f.FileHome.Kind, f.FileHome.ID)
	if err != nil {
		return err
	}
	if caller != f.WorkCaller || key != f.WorkKey || home != f.FileHome {
		return fmt.Errorf("%w: work caller, work key and file home must be trimmed", ErrInvalidAgentAccount)
	}
	return nil
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
	if err := facts.Validate(); err != nil {
		return nil, err
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
