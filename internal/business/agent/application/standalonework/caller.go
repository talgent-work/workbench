package standalonework

import (
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Caller is whom a request acts for: an owner of Standalone Works asking for
// itself, a person or an Application, or the Agent of one of a person's Works
// asking on their behalf. A Caller names a person or an Application, never
// both: an Application carries no person, not even its Application Owner.
type Caller struct {
	UserID, DisplayName string
	// ApplicationID is the Application asking for itself.
	ApplicationID string
	Agent         *ActingAgent
}

// ActingAgent is the Work an Agent asks from: its Project, if any, and its
// definition's purpose.
type ActingAgent struct{ ProjectID, Purpose string }

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

// workCaller is the Work Caller of the owner's Standalone Works: the owner
// itself, a person or an Application.
func (c Caller) workCaller() domain.WorkCaller {
	if c.ApplicationID != "" {
		return domain.WorkCaller{Kind: domain.CallerKindApplication, ID: c.ApplicationID}
	}
	return domain.WorkCaller{Kind: domain.CallerKindPerson, ID: c.UserID}
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

// libraryReader is whom a Library read is for: the person asking for
// themselves, or the owner of the Work without a Project, of the Standalone
// Work purpose, whose Agent asks. The Agent of any other Work, and an
// Application, which has no Personal Library, read no one's.
func (c Caller) libraryReader() string {
	if c.ApplicationID != "" || (c.Agent != nil && (c.Agent.ProjectID != "" || c.Agent.Purpose != domain.StandaloneWorkPurpose)) {
		return ""
	}
	return c.UserID
}

// fileReader is whom a read of a Work's files is for: the owner asking for
// itself, a person or an Application, or the person whose Library their
// Work's Agent reads.
func (c Caller) fileReader() string {
	if c.Agent != nil {
		return c.libraryReader()
	}
	return c.Owner()
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
