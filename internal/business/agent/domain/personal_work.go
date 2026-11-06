package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrNotPersonalWork      = errors.New("agent: only a Personal Work has this")
	ErrInvalidDisplayTitle  = errors.New("agent: a display title is 1 to 200 characters")
	ErrWorkFileNotAdmitted  = errors.New("agent: the person may not change this Work's files")
	ErrWorkFilesUnavailable = errors.New("agent: the Work has no File Space")
	ErrWorkFilesClosed      = errors.New("agent: a Closed Work's files no longer change")
	// ErrPersonalWorkOwnerOnly refuses a caller creating or acting on a
	// Personal Work: Agent alone creates one, and only its owner acts on it.
	ErrPersonalWorkOwnerOnly = errors.New("agent: only its owner acts on a Personal Work")
)

const maxDisplayTitleLength = 200

// Personal tells whether this is a Personal Work: a Work with neither a
// Subject nor a Project. No separate kind is recorded.
func (s *AgentWork) Personal() bool { return !s.Subject.Present() && s.ProjectID == "" }

// AdmitCallerCreation refuses a caller's creation of a Work with neither a
// Subject nor a Project: such a Work is a Personal Work, which Agent itself
// starts for its owner.
func AdmitCallerCreation(subject WorkSubject, projectID string) error {
	if !subject.Present() && projectID == "" {
		return ErrPersonalWorkOwnerOnly
	}
	return nil
}

// AdmitDelegatedControl refuses a caller acting on the owner's behalf on a
// Personal Work, which only its owner acts on.
func (s *AgentWork) AdmitDelegatedControl() error {
	if s.Personal() {
		return ErrPersonalWorkOwnerOnly
	}
	return nil
}

// DisplayTitleFrom is the display title a Personal Work starts with: the
// first line of its first input, cut to the title's length.
func DisplayTitleFrom(input string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(input), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > maxDisplayTitleLength {
		line = string([]rune(line)[:maxDisplayTitleLength])
	}
	return line
}

// validatePersonalWork keeps what a Personal Work has: no read-only mount,
// since no caller supplies one, and a display title only on it.
func (s *AgentWork) validatePersonalWork() error {
	if s.Personal() && len(s.ReadOnlyMounts) > 0 {
		return fmt.Errorf("%w: a Personal Work has no read-only mount", ErrInvalidWork)
	}
	return validateDisplayTitle(s.DisplayTitle, s.Personal())
}

func validateDisplayTitle(title string, personal bool) error {
	if title == "" {
		return nil
	}
	if !personal {
		return ErrNotPersonalWork
	}
	if strings.TrimSpace(title) != title || utf8.RuneCountInString(title) > maxDisplayTitleLength || strings.ContainsAny(title, "\n\r\x00") {
		return ErrInvalidDisplayTitle
	}
	return nil
}

// ChangeDisplayTitle replaces a Personal Work's display title at its owner's
// request; the title never identifies the Work.
func (s *AgentWork) ChangeDisplayTitle(definition *AgentDefinition, requester, title string) (bool, error) {
	if err := s.AdmitControlRequester(definition, requester); err != nil {
		return false, err
	}
	if !s.Personal() {
		return false, ErrNotPersonalWork
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return false, ErrInvalidDisplayTitle
	}
	if err := validateDisplayTitle(title, true); err != nil {
		return false, err
	}
	if s.DisplayTitle == title {
		return false, nil
	}
	s.DisplayTitle = title
	return true, nil
}

// AdmitFileChange admits a change to a Personal Work's files, whose File
// Space Agent opened: only its owner, and only until the Work is Closed.
func (s *AgentWork) AdmitFileChange(definition *AgentDefinition, requester string) error {
	if err := s.admitOwnFiles(definition, requester); err != nil {
		return err
	}
	if s.State == WorkClosed {
		return ErrWorkFilesClosed
	}
	return nil
}

// AdmitFileSharing admits creating and revoking a Personal Work's Share Links:
// its owner, at any time.
func (s *AgentWork) AdmitFileSharing(definition *AgentDefinition, requester string) error {
	return s.admitOwnFiles(definition, requester)
}

// The requester is admitted first, so a stranger learns nothing of the Work.
func (s *AgentWork) admitOwnFiles(definition *AgentDefinition, requester string) error {
	if definition == nil || definition.ID != s.AgentID || !definition.IsOwner(requester) {
		return ErrWorkFileNotAdmitted
	}
	if !s.Personal() || !s.InputSource.Present() {
		return ErrWorkFilesUnavailable
	}
	return nil
}

// ReadableWorkFile tells whether a reader admitted to a Work reads one file of
// the File Space in its input source. A Personal Work's space is its own, so
// every file is readable; another Work's space is its caller's and may hold
// that caller's other Works' files, so only the Artifacts the Work published
// itself are readable here.
func ReadableWorkFile(workID string, personal, artifact bool, publishedBy string) bool {
	return personal || (artifact && publishedBy == workID)
}

// AdmitFileReader admits a reader of the Work's files: an owner of its
// definition or a Work Share holder, who reads the files ReadableWorkFile
// allows.
func (s *AgentWork) AdmitFileReader(definition *AgentDefinition, reader string) (ReadBasis, error) {
	basis, err := s.fileReadBasis(definition, reader)
	if err != nil {
		return "", err
	}
	if !s.InputSource.Present() {
		return "", ErrWorkFilesUnavailable
	}
	return basis, nil
}

func (s *AgentWork) fileReadBasis(definition *AgentDefinition, reader string) (ReadBasis, error) {
	if reader == "" {
		return "", ErrWorkReaderNotAdmitted
	}
	if definition != nil && definition.ID == s.AgentID && definition.IsOwner(reader) {
		return ReadAsOwner, nil
	}
	if slices.Contains(s.WorkShares, reader) {
		return ReadAsShare, nil
	}
	return "", ErrWorkReaderNotAdmitted
}
