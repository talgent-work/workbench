package domain

import "strings"

// FileWriter is who makes a write in a File Space: the person the owner's
// context admitted, or a Work whose Agent made it; exactly one. Only
// PersonWriter and WorkWriter make one, so a FileWriter always names its
// writer.
type FileWriter struct {
	userID, workID string
}

// PersonWriter is the person the owner's context admitted to change a file.
func PersonWriter(userID string) (FileWriter, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return FileWriter{}, ErrFileWriterRequired
	}
	return FileWriter{userID: userID}, nil
}

// WorkWriter is a Work whose Agent changes a file.
func WorkWriter(workID string) (FileWriter, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return FileWriter{}, ErrFileWriterRequired
	}
	return FileWriter{workID: workID}, nil
}

// UserID is the writing person, empty for a Work.
func (w FileWriter) UserID() string { return w.userID }

// WorkID is the writing Work, empty for a person.
func (w FileWriter) WorkID() string { return w.workID }

func (w FileWriter) named() bool { return (w.userID == "") != (w.workID == "") }
