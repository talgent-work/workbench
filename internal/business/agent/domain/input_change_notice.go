package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInputChangeNoticeNotAdmitted refuses an input change notice to a Work
// that does not hear of that change from Agent: the change is outside its
// located inputs, or its File Home's owner tells it (File Home change
// notices).
var ErrInputChangeNoticeNotAdmitted = errors.New("agent: Agent gives this Work no notice of the input change")

// FileOperation is how a file in a Directory changed.
type FileOperation string

const (
	FileAdded    FileOperation = "added"
	FileReplaced FileOperation = "replaced"
	FileRenamed  FileOperation = "renamed"
	FileDeleted  FileOperation = "deleted"
)

// InputChange is one committed change of a file in a Directory, as Storage
// reports it: its identity, the Directory, the file's name after the change,
// a rename's former name, the operation and when it was committed.
type InputChange struct {
	ID           string
	Directory    Directory
	File         string
	PreviousFile string
	Operation    FileOperation
	At           time.Time
}

// Validate keeps a change that a notice can name: an identity, a Directory,
// a file, a known operation, a rename's former name and a time.
func (c InputChange) Validate() error {
	switch {
	case strings.TrimSpace(c.ID) == "", !c.Directory.Present(), strings.TrimSpace(c.File) == "", c.At.IsZero():
		return fmt.Errorf("%w: an input change names its identity, Directory, file and time", ErrInvalidWork)
	case c.Operation == FileRenamed && strings.TrimSpace(c.PreviousFile) == "":
		return fmt.Errorf("%w: a rename names the file's former name", ErrInvalidWork)
	}
	switch c.Operation {
	case FileAdded, FileReplaced, FileRenamed, FileDeleted:
		return nil
	}
	return fmt.Errorf("%w: unknown file operation %q", ErrInvalidWork, c.Operation)
}

// InputChangeNotice is a platform input that names only the changed file,
// the operation and the time (Input change notice): no content, path or link,
// so it stays true however long it waits in the input buffer, on either kind
// of Computer. Change is the identity of the change it tells of.
type InputChangeNotice struct {
	Change       string
	File         string
	PreviousFile string
	Operation    FileOperation
	At           time.Time
}

// Text is the notice as the Work's Agent reads it: one line, the file names
// quoted so that no name breaks it.
func (n InputChangeNotice) Text() string {
	at := n.At.UTC().Format(time.RFC3339)
	if n.Operation == FileRenamed {
		return fmt.Sprintf("Input file %q was renamed to %q at %s.", n.PreviousFile, n.File, at)
	}
	return fmt.Sprintf("Input file %q was %s at %s.", n.File, n.Operation, at)
}

// AdmitInputChangeNotice admits the notice of a change in the Work's located
// inputs while the Work has no File Home and is not Closing or Closed (Input
// change notice, Input acceptance). Agent itself delivers it, the one
// requester besides an Agent Owner or its extension that Work control
// authority and Standalone Work control admit to give a Work input. A Work
// with a File Home hears of its input changes from that File Home's owner
// instead (File Home change notices).
func (s *AgentWork) AdmitInputChangeNotice(change InputChange) (InputChangeNotice, error) {
	if err := change.Validate(); err != nil {
		return InputChangeNotice{}, err
	}
	if err := s.AdmitInput(); err != nil {
		return InputChangeNotice{}, err
	}
	inputs := s.LocatedLayout.Inputs
	if s.FileHome.Present() || inputs == nil || !inputs.Directory.Equal(change.Directory) {
		return InputChangeNotice{}, ErrInputChangeNoticeNotAdmitted
	}
	return InputChangeNotice{Change: change.ID, File: change.File, PreviousFile: change.PreviousFile, Operation: change.Operation, At: change.At}, nil
}
