package domain

import (
	"errors"
	"strings"
)

var ErrInvalidWorkSubject = errors.New("agent: invalid Work Subject")

// SubjectKind is the kind of business object a Work is about, as its caller
// defines it; Agent compares it and never interprets it.
type SubjectKind string

// WorkSubject is the optional business object a Work is about, by a kind its
// caller defines and an identity, fixed at creation. Within one
// AgentDefinition a Subject has at most one Work for good, a closed Work
// included. Agent applies uniqueness to it without interpreting
// the object it names. The zero value means none.
type WorkSubject struct {
	Kind SubjectKind
	ID   string
}

const (
	maxSubjectKindLength = 32
	maxSubjectIDLength   = 255
)

func NewWorkSubject(kind SubjectKind, id string) (WorkSubject, error) {
	subject := WorkSubject{Kind: SubjectKind(strings.TrimSpace(string(kind))), ID: strings.TrimSpace(id)}
	return subject, subject.Validate()
}

// Present tells whether the Work has a Subject.
func (s WorkSubject) Present() bool { return s != WorkSubject{} }

// Validate accepts no Subject, or a kind together with an identity.
func (s WorkSubject) Validate() error {
	if !s.Present() {
		return nil
	}
	kind := string(s.Kind)
	if kind == "" || len(kind) > maxSubjectKindLength || strings.TrimSpace(kind) != kind || strings.ContainsRune(kind, 0) {
		return ErrInvalidWorkSubject
	}
	if s.ID == "" || len(s.ID) > maxSubjectIDLength || strings.TrimSpace(s.ID) != s.ID || strings.ContainsRune(s.ID, 0) {
		return ErrInvalidWorkSubject
	}
	return nil
}
