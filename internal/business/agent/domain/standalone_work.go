package domain

import (
	"errors"
	"fmt"
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

// AdmitDelegatedControl admits an extension acting on the owner's behalf
// only on an Extension Work, whose Work Caller is of a kind an extension
// defines (Delegated control); a Standalone Work only its owner acts on. A
// stored Work always holds a valid Work Caller, so an invalid one is a fault
// of the store, never of the request.
func (s *AgentWork) AdmitDelegatedControl() error {
	if err := s.WorkCaller.Validate(); err != nil {
		return fmt.Errorf("agent: Work %s holds an invalid Work Caller: %v", s.ID, err)
	}
	if !s.WorkCaller.ExtensionDefined() {
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
// its File Spaces (Shared space reading). A Work without a File Home keeps
// its files in its own File Spaces, so every file is readable; a Work with
// one reads its inputs from and publishes into its File Home's File Spaces,
// which hold that File Home's other Works' files, so only the Artifacts the
// Work published itself are readable there.
func ReadableWorkFile(workID string, inFileHome, artifact bool, publishedBy string) bool {
	return !inFileHome || (artifact && publishedBy == workID)
}

// FileReader is who reads a Work's files: an Account by its id, or the Agent
// of a Work by that Work's identity.
type FileReader struct {
	account string
	agentOf string
}

// AccountReader is a person or an Application reading for itself, or the
// person whose Library their Work's Agent reads.
func AccountReader(id string) FileReader { return FileReader{account: id} }

// AgentReader is the Agent of the Work, by that Work's identity.
func AgentReader(workID string) FileReader { return FileReader{agentOf: workID} }

// AdmitFileReader admits a reader of the Work's files: an owner of its
// definition or a Work Share holder, who reads the files ReadableWorkFile
// allows, or the Work's own Agent, which reads the current Attachments of
// its located inputs, whatever its File Home and Computer (Own input
// reading, Managed input reading), as LocatedInputs names them.
func (s *AgentWork) AdmitFileReader(definition *AgentDefinition, reader FileReader) (ReadBasis, error) {
	if reader.agentOf != "" {
		if reader.agentOf != s.ID {
			return "", ErrWorkReaderNotAdmitted
		}
		return ReadAsOwnAgent, nil
	}
	if reader.account == "" {
		return "", ErrWorkReaderNotAdmitted
	}
	if definition != nil && definition.ID == s.AgentID && definition.IsOwner(reader.account) {
		return ReadAsOwner, nil
	}
	if slices.Contains(s.WorkShares, reader.account) {
		return ReadAsShare, nil
	}
	return "", ErrWorkReaderNotAdmitted
}

// LocatedInputs is the Directory the Work reads its inputs from: its own File
// Space, or its File Home's for a Work with one.
func (s *AgentWork) LocatedInputs() (Directory, error) {
	if s.LocatedLayout.Inputs == nil {
		return Directory{}, ErrWorkFilesUnavailable
	}
	return s.LocatedLayout.Inputs.Directory.clone(), nil
}
