package standalonework

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Caller is whom a request acts for: an owner of Standalone Works asking for
// itself, a person or an Application, or the Agent of a Work asking from it.
// A Caller names a person, an Application or an acting Work, never two: an
// Application carries no person, not even its Application Owner.
type Caller struct {
	UserID, DisplayName string
	// ApplicationID is the Application asking for itself.
	ApplicationID string
	Agent         *ActingAgent
}

// ActingAgent is the Work an Agent asks from, by its identity: Agent reads
// that Work's own facts, never facts the request carries about it.
type ActingAgent struct{ WorkID string }

// Owner is the owner asking for its own Works, a person or an Application;
// an Agent asks for no one here.
func (c Caller) Owner() string {
	if c.Agent != nil {
		return ""
	}
	if c.ApplicationID != "" {
		return c.ApplicationID
	}
	return c.UserID
}

// ownerWorkCaller is the Work Caller of the owner's Standalone Works: the
// Application or the person asking for itself.
func (c Caller) ownerWorkCaller() domain.WorkCaller {
	if c.ApplicationID != "" {
		return domain.WorkCaller{Kind: domain.CallerKindApplication, ID: c.ApplicationID}
	}
	return domain.WorkCaller{Kind: domain.CallerKindPerson, ID: c.Owner()}
}

// self is the person asking for themselves, for what only a person has: Work
// Shares, repository choices and Share Links. An Application and an Agent are
// no one here.
func (c Caller) self() string {
	if c.Agent != nil || c.ApplicationID != "" {
		return ""
	}
	return c.UserID
}

// fileActor is the owner changing or sharing its Standalone Works' files, a
// person or an Application; an Agent changes none, so it names no one.
func (c Caller) fileActor() command.FileActor {
	if c.Agent != nil {
		return command.FileActor{}
	}
	if c.ApplicationID != "" {
		return command.FileActor{ApplicationID: c.ApplicationID}
	}
	return command.FileActor{UserID: c.UserID}
}

// libraryReader is whom a Library read is for (Library readers): the person
// asking for themselves, or the person who is the Work Caller of the Work
// whose Agent asks, as that Work's own facts state. The Agent of any other
// Work, and an Application, which has no Personal Library, read no one's.
func (p *StandaloneWorks) libraryReader(ctx context.Context, caller Caller) (string, error) {
	switch {
	case caller.ApplicationID != "":
		return "", nil
	case caller.Agent == nil:
		return caller.UserID, nil
	}
	origin, err := p.works.GetWorkOrigin(ctx, caller.Agent.WorkID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if origin.WorkCaller.Kind != domain.CallerKindPerson {
		return "", nil
	}
	return origin.WorkCaller.ID, nil
}

// fileReader is whom a read of a Work's files is for: the owner asking for
// itself, a person or an Application, or the person whose Library their
// Work's Agent reads.
func (p *StandaloneWorks) fileReader(ctx context.Context, caller Caller) (string, error) {
	if caller.Agent != nil {
		return p.libraryReader(ctx, caller)
	}
	return caller.Owner(), nil
}
