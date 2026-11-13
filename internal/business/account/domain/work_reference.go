package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of a Work Caller, Work Key or File Home as Account keeps and signs
// it: its kind never holds ":", so "<kind>:<id>" splits at its first colon.
//
// Agent applies the same "<kind>:<id>" shape to the facts it supplies.
// Account keeps its own copy of the rule because it is upstream of Agent and
// must not import Agent's model; both follow the contract's shape.
const (
	maxReferenceKindLength = 32
	maxReferenceIDLength   = 255
)

// WorkCaller is the party in whose name the Work was started, as a kind and an
// identity, such as a person, an Application or a Project. Every Work has one.
type WorkCaller struct {
	Kind, ID string
}

// NewWorkCaller admits a Work Caller exactly as supplied; both its kind and
// its identity are required.
func NewWorkCaller(kind, id string) (WorkCaller, error) {
	return WorkCaller{Kind: kind, ID: id}, checkReference("work caller", kind, id, true)
}

// String is the Work Caller as "<kind>:<id>", the claim every token states.
func (c WorkCaller) String() string { return referenceString(c.Kind, c.ID) }

// WorkKey is the optional name the Work's extension finds it by again, as a
// kind and an identity. The zero value means none.
type WorkKey struct {
	Kind, ID string
}

// NewWorkKey admits a Work Key exactly as supplied; an empty kind and
// identity mean none.
func NewWorkKey(kind, id string) (WorkKey, error) {
	return WorkKey{Kind: kind, ID: id}, checkReference("work key", kind, id, false)
}

// String is the Work Key as "<kind>:<id>"; empty for none.
func (k WorkKey) String() string { return referenceString(k.Kind, k.ID) }

// FileHome is the optional owner of the shared File Spaces the Work reads its
// inputs from and publishes into, as a kind and an identity. The zero value
// means none.
type FileHome struct {
	Kind, ID string
}

// NewFileHome admits a File Home exactly as supplied; an empty kind and
// identity mean none.
func NewFileHome(kind, id string) (FileHome, error) {
	return FileHome{Kind: kind, ID: id}, checkReference("file home", kind, id, false)
}

// String is the File Home as "<kind>:<id>"; empty for none.
func (h FileHome) String() string { return referenceString(h.Kind, h.ID) }

// checkReference bounds what Account stores and signs, and never rewrites or
// interprets it (Facts carried, not read): a fact is carried exactly or
// refused. The entry writes each fact as a header, so neither part may hold
// surrounding whitespace or a control character.
func checkReference(name, kind, id string, required bool) error {
	if kind == "" && id == "" && !required {
		return nil
	}
	if kind == "" || id == "" {
		return fmt.Errorf("%w: a %s needs both its kind and its id", ErrInvalidAgentAccount, name)
	}
	for _, part := range [...]struct{ name, value string }{{"kind", kind}, {"id", id}} {
		if strings.TrimSpace(part.value) != part.value {
			return fmt.Errorf("%w: a %s %s has surrounding whitespace", ErrInvalidAgentAccount, name, part.name)
		}
		if strings.IndexFunc(part.value, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: a %s %s holds a control character", ErrInvalidAgentAccount, name, part.name)
		}
	}
	if strings.Contains(kind, ":") {
		return fmt.Errorf("%w: a %s kind holds ':'", ErrInvalidAgentAccount, name)
	}
	if utf8.RuneCountInString(kind) > maxReferenceKindLength {
		return fmt.Errorf("%w: a %s kind exceeds %d characters", ErrInvalidAgentAccount, name, maxReferenceKindLength)
	}
	if utf8.RuneCountInString(id) > maxReferenceIDLength {
		return fmt.Errorf("%w: a %s id exceeds %d characters", ErrInvalidAgentAccount, name, maxReferenceIDLength)
	}
	return nil
}

func referenceString(kind, id string) string {
	if kind == "" {
		return ""
	}
	return kind + ":" + id
}
