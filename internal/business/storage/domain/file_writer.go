package domain

import "strings"

// FileWriter is who makes a write in a File Space: the person or the
// Application the owner's context admitted, or a Work whose Agent made it;
// exactly one. Only AdmittedWriter and WorkWriter make one, so a FileWriter
// always names its writer.
type FileWriter struct {
	userID, applicationID, workID string
}

// AdmittedWriter is the person or the Application the owner's context
// admitted to change a file; it names exactly one of them.
func AdmittedWriter(userID, applicationID string) (FileWriter, error) {
	userID, applicationID = strings.TrimSpace(userID), strings.TrimSpace(applicationID)
	if (userID == "") == (applicationID == "") {
		return FileWriter{}, ErrFileWriterRequired
	}
	return FileWriter{userID: userID, applicationID: applicationID}, nil
}

// WorkWriter is a Work whose Agent changes a file.
func WorkWriter(workID string) (FileWriter, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return FileWriter{}, ErrFileWriterRequired
	}
	return FileWriter{workID: workID}, nil
}

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
