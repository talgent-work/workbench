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

// WorkSubject is the one business object a Work is about, by its caller's
// kind and identity; both are empty for a Work without one.
type WorkSubject struct {
	Kind string
	ID   string
}

// WorkFacts are a Work's fixed facts as Agent supplies them. Account copies
// them into Identity Tokens and decides nothing from them (Facts carried, not
// read).
type WorkFacts struct {
	// ProjectID is empty for a Work without a Project.
	ProjectID string
	AgentID   string
	Purpose   string
	Subject   WorkSubject
	// RepresentedPerson is empty for an Agent that represents no one (No
	// person for an unrepresented Agent).
	RepresentedPerson AccountID
	// OwningApplication is the Application that owns the Work; empty for a
	// Work no Application owns.
	OwningApplication ApplicationID
	// WorkLabels are the Work's Work Labels; empty for none.
	WorkLabels WorkLabels
}

// maxFactLength bounds each fact, as the column that keeps it.
const maxFactLength = 255

// NewWorkFacts trims and validates the facts Agent supplies.
func NewWorkFacts(projectID, agentID, purpose, subjectKind, subjectID, representedPerson, owningApplication string, workLabels map[string]string) (WorkFacts, error) {
	facts := WorkFacts{
		ProjectID: strings.TrimSpace(projectID),
		AgentID:   strings.TrimSpace(agentID),
		Purpose:   strings.TrimSpace(purpose),
		Subject:   WorkSubject{Kind: strings.TrimSpace(subjectKind), ID: strings.TrimSpace(subjectID)},
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

// Equal reports whether two statements of a Work's facts are the same.
func (f WorkFacts) Equal(other WorkFacts) bool {
	return f.ProjectID == other.ProjectID && f.AgentID == other.AgentID && f.Purpose == other.Purpose && f.Subject == other.Subject &&
		f.RepresentedPerson == other.RepresentedPerson && f.OwningApplication == other.OwningApplication && f.WorkLabels.Equal(other.WorkLabels)
}

func (f WorkFacts) Validate() error {
	if f.AgentID == "" || f.Purpose == "" {
		return fmt.Errorf("%w: agent definition and purpose are required", ErrInvalidAgentAccount)
	}
	if (f.Subject.Kind == "") != (f.Subject.ID == "") {
		return fmt.Errorf("%w: a subject needs both its kind and its id", ErrInvalidAgentAccount)
	}
	for _, fact := range []string{f.ProjectID, f.AgentID, f.Purpose, f.Subject.Kind, f.Subject.ID} {
		if utf8.RuneCountInString(fact) > maxFactLength {
			return fmt.Errorf("%w: a fact exceeds %d characters", ErrInvalidAgentAccount, maxFactLength)
		}
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
	if !facts.Equal(a.Facts) {
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
