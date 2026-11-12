package domain

import "strings"

// Actor is the person or the Application the owner's context admitted to
// change or share a file; a valid one names exactly one of them.
type Actor struct{ UserID, ApplicationID string }

// Validate requires exactly one of the person and the Application.
func (a Actor) Validate() error {
	if (strings.TrimSpace(a.UserID) == "") == (strings.TrimSpace(a.ApplicationID) == "") {
		return ErrActorRequired
	}
	return nil
}

func (a Actor) trimmed() Actor {
	return Actor{UserID: strings.TrimSpace(a.UserID), ApplicationID: strings.TrimSpace(a.ApplicationID)}
}

// FileWriter is who makes a write in a File Space: an Actor, or a Work whose
// Agent made it; exactly one. Only AdmittedWriter and WorkWriter make one, so
// a FileWriter always names its writer.
type FileWriter struct {
	userID, applicationID, workID string
}

// AdmittedWriter is the Actor the owner's context admitted to change a file.
func AdmittedWriter(actor Actor) (FileWriter, error) {
	if err := actor.Validate(); err != nil {
		return FileWriter{}, ErrFileWriterRequired
	}
	actor = actor.trimmed()
	return FileWriter{userID: actor.UserID, applicationID: actor.ApplicationID}, nil
}

// WorkWriter is a Work whose Agent changes a file.
func WorkWriter(workID string) (FileWriter, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return FileWriter{}, ErrFileWriterRequired
	}
	return FileWriter{workID: workID}, nil
}

// Actor is the writing person or Application, empty for a Work.
func (w FileWriter) Actor() Actor { return Actor{UserID: w.userID, ApplicationID: w.applicationID} }

// UserID is the writing person, empty for an Application or a Work.
func (w FileWriter) UserID() string { return w.userID }

// ApplicationID is the writing Application, empty for a person or a Work.
func (w FileWriter) ApplicationID() string { return w.applicationID }

// WorkID is the writing Work, empty for a person or an Application.
func (w FileWriter) WorkID() string { return w.workID }

func (w FileWriter) named() bool {
	named := 0
	for _, id := range []string{w.userID, w.applicationID, w.workID} {
		if id != "" {
			named++
		}
	}
	return named == 1
}
