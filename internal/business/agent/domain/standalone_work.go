package domain

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrNotStandaloneWork    = errors.New("agent: only a Standalone Work has this")
	ErrInvalidDisplayTitle  = errors.New("agent: a display title is 1 to 200 characters")
	ErrWorkFileNotAdmitted  = errors.New("agent: the person may not change this Work's files")
	ErrWorkFilesUnavailable = errors.New("agent: the Work has no File Space")
	ErrWorkFilesClosed      = errors.New("agent: a Closed Work's files no longer change")
	// ErrStandaloneWorkOwnerOnly refuses an extension creating or acting on a
	// Standalone Work: only its owner starts one and acts on it.
	ErrStandaloneWorkOwnerOnly = errors.New("agent: only its owner acts on a Standalone Work")
)

const maxDisplayTitleLength = 200

// startingTitleLength bounds the title a Work takes from its first input: a
// first line is often the whole instruction, too long to name the Work by.
const startingTitleLength = 80

// Standalone tells whether this is a Standalone Work: a Work whose Work
// Caller is a person or an Application, which starts it for itself.
func (s *AgentWork) Standalone() bool { return s.WorkCaller.StartsStandaloneWork() }

// validateWorkFacts keeps the facts fixed at creation valid: a Work Caller,
// and a Work Key and a File Home only beside a Work Caller of an
// extension-defined kind (Standalone Work facts).
func (s *AgentWork) validateWorkFacts() error {
	if err := s.WorkCaller.Validate(); err != nil {
		return errors.Join(ErrInvalidWork, err)
	}
	if err := s.WorkKey.Validate(); err != nil {
		return errors.Join(ErrInvalidWork, err)
	}
	if err := s.FileHome.Validate(); err != nil {
		return errors.Join(ErrInvalidWork, err)
	}
	if s.Standalone() && (s.WorkKey.Present() || s.FileHome.Present()) {
		return errors.Join(ErrInvalidWork, ErrStandaloneWorkFacts)
	}
	return nil
}

// AdmitDelegatedControl refuses an extension acting on the owner's behalf on
// a Standalone Work, which only its owner acts on.
func (s *AgentWork) AdmitDelegatedControl() error {
	if s.Standalone() {
		return ErrStandaloneWorkOwnerOnly
	}
	return nil
}

// DisplayTitleFrom is the display title a Standalone Work starts with: the
// first line of its first input, cut with an ellipsis when longer than a
// starting title.
func DisplayTitleFrom(input string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(input), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > startingTitleLength {
		line = strings.TrimSpace(string([]rune(line)[:startingTitleLength-1])) + "…"
	}
	return line
}

func validateDisplayTitle(title string, standalone bool) error {
	if title == "" {
		return nil
	}
	if !standalone {
		return ErrNotStandaloneWork
	}
	if strings.TrimSpace(title) != title || utf8.RuneCountInString(title) > maxDisplayTitleLength || strings.ContainsAny(title, "\n\r\x00") {
		return ErrInvalidDisplayTitle
	}
	return nil
}

// ChangeDisplayTitle replaces a Standalone Work's display title at its owner's
// request; the title never identifies the Work.
func (s *AgentWork) ChangeDisplayTitle(definition *AgentDefinition, requester, title string) (bool, error) {
	if err := s.AdmitControlRequester(definition, requester); err != nil {
		return false, err
	}
	if !s.Standalone() {
		return false, ErrNotStandaloneWork
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

// AdmitFileChange admits a change to a Standalone Work's files: only its owner,
// and only until the Work is Closed.
func (s *AgentWork) AdmitFileChange(definition *AgentDefinition, requester string) error {
	if err := s.admitOwnFiles(definition, requester); err != nil {
		return err
	}
	if s.State == WorkClosed {
		return ErrWorkFilesClosed
	}
	return nil
}

// AdmitFileSharing admits creating and revoking a Standalone Work's Share Links:
// its owner, at any time.
func (s *AgentWork) AdmitFileSharing(definition *AgentDefinition, requester string) error {
	return s.admitOwnFiles(definition, requester)
}

// The requester is admitted first, so a stranger learns nothing of the Work.
func (s *AgentWork) admitOwnFiles(definition *AgentDefinition, requester string) error {
	if definition == nil || definition.ID != s.AgentID || !definition.IsOwner(requester) {
		return ErrWorkFileNotAdmitted
	}
	if _, err := s.StandaloneFileSpaces(); err != nil {
		return err
	}
	return nil
}

// StandaloneFiles are a Standalone Work's own permanent File Spaces (rule Personal
// File Space): its inputs, `{Work}, inputs`, and its publication target,
// `{Work}, artifacts`.
type StandaloneFiles struct {
	Inputs, Artifacts LocatedDirectory
}

// StandaloneFileSpaces names a Standalone Work's File Spaces; an Extension
// Work's files are its extension's.
func (s *AgentWork) StandaloneFileSpaces() (StandaloneFiles, error) {
	if !s.Standalone() || s.LocatedLayout.Inputs == nil {
		return StandaloneFiles{}, ErrWorkFilesUnavailable
	}
	return StandaloneFiles{Inputs: s.LocatedLayout.Inputs.clone(), Artifacts: s.LocatedLayout.PublicationTarget.clone()}, nil
}

// ReadableWorkFile tells whether a reader admitted to a Work reads one file of
// its File Spaces. A Standalone Work's spaces are its own, so every file is
// readable; an Extension Work may publish into its File Home's File Space,
// which holds that File Home's other Works' files, so only the Artifacts the
// Work published itself are readable here.
func ReadableWorkFile(workID string, standalone, artifact bool, publishedBy string) bool {
	return standalone || (artifact && publishedBy == workID)
}

// AdmitFileReader admits a reader of the Work's files: an owner of its
// definition or a Work Share holder, who reads the files ReadableWorkFile
// allows.
func (s *AgentWork) AdmitFileReader(definition *AgentDefinition, reader string) (ReadBasis, error) {
	return s.fileReadBasis(definition, reader)
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
