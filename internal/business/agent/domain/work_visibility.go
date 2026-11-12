package domain

import (
	"errors"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidVisibility     = errors.New("agent: invalid Work visibility")
	ErrWorkVisibilityFixed   = errors.New("agent: Works of a definition that represents no person keep their declared visibility")
	ErrWorkWithoutProject    = errors.New("agent: a Work without a Project stays private")
	ErrWorkShareOutOfScope   = errors.New("agent: a Work Share reader must be within the Work's visibility scope")
	ErrWorkReaderNotAdmitted = errors.New("agent: reader is not admitted to the Work's message stream")
)

// VisibilityRule declares the visibility of a definition's new Works:
// private, or public within the Work's Project. Existing Works keep the rule
// they captured.
type VisibilityRule struct {
	Public bool
}

// WorkVisibility is a Work's current visibility, captured from its
// definition's VisibilityRule: private, or public within the Work's Project.
// Visibility never crosses that Project.
type WorkVisibility struct {
	Public bool
}

// ReaderAdmission states the scope a reader is within: a trusted caller's
// statement that the reader is a member of a Project, or Agent's own finding
// that the reader is in the User Directory, the scope of a Work without a
// Project, since a deployment is one enterprise. Its zero value admits
// nothing.
type ReaderAdmission struct {
	ProjectID string
	Directory bool
}

// ReadBasis is why Agent admits a reader to a Work's message stream.
type ReadBasis string

const (
	ReadAsOwner  ReadBasis = "owner"
	ReadAsShare  ReadBasis = "share"
	ReadAsPublic ReadBasis = "public"
	// ReadAsApplicationOwner is the Application Owner of the Application whose
	// Work it is, who reads it and acts on none of it.
	ReadAsApplicationOwner ReadBasis = "application_owner"
)

// ChangeVisibility switches the Work between private and public within its
// Project. Only an Owner of a person's Agent changes it; a Work without a
// Project has no scope to be public in.
func (s *AgentWork) ChangeVisibility(definition *AgentDefinition, requester string, public bool, now time.Time) (bool, error) {
	if err := s.AdmitControlRequester(definition, requester); err != nil {
		return false, err
	}
	if !definition.RepresentsHuman() {
		return false, ErrWorkVisibilityFixed
	}
	if s.Visibility.Public == public {
		return false, nil
	}
	if public && s.ProjectID == "" {
		return false, ErrWorkWithoutProject
	}
	s.Visibility.Public = public
	if !public {
		s.record(WorkMadePrivate{WorkID: s.ID, Owners: slices.Clone(definition.Owners), ShareReaderIDs: slices.Clone(s.WorkShares), OccurredAt: now.UTC()})
	}
	return true, nil
}

// GrantWorkShare lets one reader read this human's Work while it is private.
// The trusted caller's admission states the reader is within the value of the
// Work's visibility scope, such as a member of its Project; granting an
// existing reader leaves WorkShares unchanged.
func (s *AgentWork) GrantWorkShare(definition *AgentDefinition, requester, reader string, admission ReaderAdmission) (bool, error) {
	if err := s.AdmitControlRequester(definition, requester); err != nil {
		return false, err
	}
	if !definition.RepresentsHuman() {
		return false, ErrWorkVisibilityFixed
	}
	if reader = strings.TrimSpace(reader); reader == "" || len(reader) > 255 || definition.IsOwner(reader) {
		return false, ErrInvalidVisibility
	}
	if !s.withinScope(admission) {
		return false, ErrWorkShareOutOfScope
	}
	if slices.Contains(s.WorkShares, reader) {
		return false, nil
	}
	s.WorkShares = append(slices.Clone(s.WorkShares), reader)
	return true, nil
}

// RevokeWorkShare removes a reader at the owner's request.
func (s *AgentWork) RevokeWorkShare(definition *AgentDefinition, requester, reader string, now time.Time) (bool, error) {
	if err := s.AdmitControlRequester(definition, requester); err != nil {
		return false, err
	}
	return s.revokeShare(reader, now), nil
}

// RevokeDepartedReader removes a reader the trusted caller reports has left
// the Work's visibility scope, such as a member who left its Project. A
// departure from another scope leaves the Work Share in place.
func (s *AgentWork) RevokeDepartedReader(reader string, departed ReaderAdmission, now time.Time) bool {
	if !s.withinScope(departed) {
		return false
	}
	return s.revokeShare(reader, now)
}

func (s *AgentWork) revokeShare(reader string, now time.Time) bool {
	index := slices.Index(s.WorkShares, reader)
	if index < 0 {
		return false
	}
	s.WorkShares = slices.Delete(slices.Clone(s.WorkShares), index, index+1)
	s.record(WorkShareRevoked{WorkID: s.ID, ReaderID: reader, OccurredAt: now.UTC()})
	return true
}

// AdmitStreamReader admits an Owner of the Work's definition; a Work Share
// holder while private; or, while public, a reader the trusted caller admits
// as a member of the Work's Project.
func (s *AgentWork) AdmitStreamReader(definition *AgentDefinition, reader string, admission ReaderAdmission) (ReadBasis, error) {
	if reader == "" {
		return "", ErrWorkReaderNotAdmitted
	}
	if definition != nil && definition.ID == s.AgentID && definition.IsOwner(reader) {
		return ReadAsOwner, nil
	}
	if s.Visibility.Public {
		if s.withinScope(admission) {
			return ReadAsPublic, nil
		}
		return "", ErrWorkReaderNotAdmitted
	}
	if slices.Contains(s.WorkShares, reader) {
		return ReadAsShare, nil
	}
	return "", ErrWorkReaderNotAdmitted
}

// AdmitApplicationOwnerReader admits the Application Owner of the Application
// whose Agent runs the Work to read its message stream and files, also after
// the Application's removal; reading admits no control (Application Owner
// reading).
func (s *AgentWork) AdmitApplicationOwnerReader(definition *AgentDefinition, requester DefinitionRequester) (ReadBasis, error) {
	if definition == nil || definition.ID != s.AgentID || !definition.admitsApplicationOwner(requester) {
		return "", ErrWorkReaderNotAdmitted
	}
	return ReadAsApplicationOwner, nil
}

// withinScope reports whether an admission names the Work's visibility
// scope: its Project, or the User Directory for a Work without one.
func (s *AgentWork) withinScope(admission ReaderAdmission) bool {
	if s.ProjectID == "" {
		return admission.Directory
	}
	return admission.ProjectID != "" && admission.ProjectID == s.ProjectID
}

func (s *AgentWork) record(event Event) {
	if s.Events == nil {
		s.Events = NewEventCollection()
	}
	s.Events.Add(event)
}

func (s *AgentWork) validateVisibility() error {
	seen := make(map[string]bool, len(s.WorkShares))
	for _, reader := range s.WorkShares {
		if strings.TrimSpace(reader) == "" || seen[reader] {
			return ErrInvalidVisibility
		}
		seen[reader] = true
	}
	return nil
}
