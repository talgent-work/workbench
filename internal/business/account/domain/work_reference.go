package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of a Work Caller, Work Key or File Home as Account keeps and signs
// it: its kind never holds ":", so "<kind>:<id>" splits at its first colon.
const (
	maxReferenceKindLength = 32
	maxReferenceIDLength   = 255
)

// WorkCaller is the party in whose name the Work was started, as a kind and an
// identity, such as a person, an Application or a Project. Every Work has one.
type WorkCaller struct {
	Kind, ID string
}

// NewWorkCaller trims and bounds a Work Caller; both its kind and its identity
// are required.
func NewWorkCaller(kind, id string) (WorkCaller, error) {
	kind, id, err := newReference("work caller", kind, id, true)
	return WorkCaller{Kind: kind, ID: id}, err
}

// String is the Work Caller as "<kind>:<id>", the claim every token states.
func (c WorkCaller) String() string { return referenceString(c.Kind, c.ID) }

// WorkKey is the optional name the Work's extension finds it by again, as a
// kind and an identity. The zero value means none.
type WorkKey struct {
	Kind, ID string
}

// NewWorkKey trims and bounds a Work Key; an empty kind and identity mean none.
func NewWorkKey(kind, id string) (WorkKey, error) {
	kind, id, err := newReference("work key", kind, id, false)
	return WorkKey{Kind: kind, ID: id}, err
}

// String is the Work Key as "<kind>:<id>"; empty for none.
func (k WorkKey) String() string { return referenceString(k.Kind, k.ID) }

// FileHome is the optional owner of the shared File Spaces the Work reads its
// inputs from and publishes into, as a kind and an identity. The zero value
// means none.
type FileHome struct {
	Kind, ID string
}

// NewFileHome trims and bounds a File Home; an empty kind and identity mean
// none.
func NewFileHome(kind, id string) (FileHome, error) {
	kind, id, err := newReference("file home", kind, id, false)
	return FileHome{Kind: kind, ID: id}, err
}

// String is the File Home as "<kind>:<id>"; empty for none.
func (h FileHome) String() string { return referenceString(h.Kind, h.ID) }

// newReference trims a kind and an identity and bounds what Account stores
// and signs; it never interprets either (Facts carried, not read). Neither
// may hold a control character, since the entry writes each as a header.
func newReference(name, kind, id string, required bool) (string, string, error) {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	if kind == "" && id == "" && !required {
		return "", "", nil
	}
	if kind == "" || id == "" {
		return "", "", fmt.Errorf("%w: a %s needs both its kind and its id", ErrInvalidAgentAccount, name)
	}
	if utf8.RuneCountInString(kind) > maxReferenceKindLength || strings.Contains(kind, ":") || hasControl(kind) {
		return "", "", fmt.Errorf("%w: a %s kind is at most %d characters without ':'", ErrInvalidAgentAccount, name, maxReferenceKindLength)
	}
	if utf8.RuneCountInString(id) > maxReferenceIDLength || hasControl(id) {
		return "", "", fmt.Errorf("%w: a %s id is at most %d characters", ErrInvalidAgentAccount, name, maxReferenceIDLength)
	}
	return kind, id, nil
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func referenceString(kind, id string) string {
	if kind == "" {
		return ""
	}
	return kind + ":" + id
}
