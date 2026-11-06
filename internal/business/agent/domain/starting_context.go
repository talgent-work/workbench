package domain

import (
	"context"
	"errors"
	"strings"
)

// ErrStartingContextNotAdmitted refuses starting a Work from the context
// of a Work of another AgentDefinition or Project, or without a position.
var ErrStartingContextNotAdmitted = errors.New("agent: a Work starts only from the context of another Work of its own definition and Project")

// StartingContextSource is a caller's request that a new Work start from an
// existing Work's context up to a position Agent named for one of its calls.
type StartingContextSource struct {
	WorkID   string
	Position string
}

// StartingContext is the context a Work started from, captured at creation
// and never changed: another Work of the same definition, its Harness Session
// at that time, and the position Agent named in it. The two Works then
// continue apart, and the new Work's message stream holds only its own
// messages.
type StartingContext struct {
	SourceWorkID           string
	SourceSessionReference string
	Position               string
}

// startingContext admits a source Work of this definition and Project only.
func (a *AgentDefinition) startingContext(ctx context.Context, source *StartingContextSource, projectID string, lookup WorkLookupPort) (*StartingContext, error) {
	if source == nil {
		return nil, nil
	}
	position := strings.TrimSpace(source.Position)
	work, err := lookup.LocateWork(ctx, strings.TrimSpace(source.WorkID))
	if errors.Is(err, ErrWorkNotFound) {
		return nil, ErrStartingContextNotAdmitted
	}
	if err != nil {
		return nil, err
	}
	if work == nil || work.AgentID != a.ID || work.ProjectID != strings.TrimSpace(projectID) || position == "" || work.HarnessSessionReference == "" {
		return nil, ErrStartingContextNotAdmitted
	}
	return &StartingContext{SourceWorkID: work.ID, SourceSessionReference: work.HarnessSessionReference, Position: position}, nil
}

func (c *StartingContext) clone() *StartingContext {
	if c == nil {
		return nil
	}
	copied := *c
	return &copied
}

func (c *StartingContext) validate() error {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(c.SourceWorkID) == "" || strings.TrimSpace(c.SourceSessionReference) == "" || strings.TrimSpace(c.Position) == "" {
		return ErrInvalidWork
	}
	return nil
}
