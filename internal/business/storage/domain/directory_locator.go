// Package domain holds Storage's Directory with its Share Links, the
// Preview Copy of a content, Preview Policy, and the rules of the paths and
// layouts a caller names. The files themselves are facts of the backends.
package domain

import (
	"strings"
	"unicode/utf8"
)

// WorkCondition is the one condition Storage interprets: its value is a
// Work's identity, and a Directory with its Work's lifetime is reclaimed
// with that Work.
const WorkCondition = "work"

// OwnerFact is one condition of a Directory Locator with the caller's value.
type OwnerFact struct {
	Condition string
	Value     string
}

// DirectoryOwner is a Locator's conditions with the caller's fact values,
// such as {subject: case:1}, kept as supplied in condition order. Storage
// interprets only whether it names a Work. It compares by value.
type DirectoryOwner struct {
	// facts is the canonical form: each fact as condition, unit separator,
	// value, facts joined by the record separator. Neither separator is ever
	// part of a fact.
	facts string
}

const (
	factSeparator  = "\x1e"
	valueSeparator = "\x1f"
)

// NewDirectoryOwner admits the facts of a located owner: at least one,
// ordered by condition name with each condition once, none empty and none
// holding a control character.
func NewDirectoryOwner(facts []OwnerFact) (DirectoryOwner, error) {
	if len(facts) == 0 {
		return DirectoryOwner{}, ErrInvalidLocator
	}
	written := make([]string, 0, len(facts))
	for i, fact := range facts {
		if !visibleText(fact.Condition) || !visibleText(fact.Value) || i > 0 && facts[i-1].Condition >= fact.Condition {
			return DirectoryOwner{}, ErrInvalidLocator
		}
		written = append(written, fact.Condition+valueSeparator+fact.Value)
	}
	return DirectoryOwner{facts: strings.Join(written, factSeparator)}, nil
}

// maxFactBytes bounds one condition or value.
const maxFactBytes = 255

func visibleText(text string) bool {
	if text == "" || len(text) > maxFactBytes || !utf8.ValidString(text) || strings.TrimSpace(text) != text {
		return false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Facts are the owner's facts in condition order.
func (o DirectoryOwner) Facts() []OwnerFact {
	if o.facts == "" {
		return nil
	}
	written := strings.Split(o.facts, factSeparator)
	facts := make([]OwnerFact, 0, len(written))
	for _, fact := range written {
		condition, value, _ := strings.Cut(fact, valueSeparator)
		facts = append(facts, OwnerFact{Condition: condition, Value: value})
	}
	return facts
}

// Work is the Work the owner names, if it names one.
func (o DirectoryOwner) Work() (string, bool) {
	for _, fact := range o.Facts() {
		if fact.Condition == WorkCondition {
			return fact.Value, true
		}
	}
	return "", false
}

// IsZero reports whether the owner was never admitted.
func (o DirectoryOwner) IsZero() bool { return o.facts == "" }

// DirectoryID is a Directory's identity: its owner and its name, distinct
// within the owner. It compares by value.
type DirectoryID struct {
	Owner DirectoryOwner
	Name  string
}

// NewDirectoryID admits a name of one visible path element within an owner.
func NewDirectoryID(owner DirectoryOwner, name string) (DirectoryID, error) {
	if owner.IsZero() || strings.HasPrefix(name, ".") || ValidateFileName(name) != nil {
		return DirectoryID{}, ErrInvalidLocator
	}
	return DirectoryID{Owner: owner, Name: name}, nil
}

// Backend is where a Directory's files live, fixed at its first allocation.
type Backend string

const (
	BackendNAS     Backend = "nas"
	BackendJuiceFS Backend = "juicefs"
)

// Valid reports whether Storage keeps Directories on the backend.
func (b Backend) Valid() bool { return b == BackendNAS || b == BackendJuiceFS }

// Lifetime is how long a Directory is kept, fixed at its first allocation.
type Lifetime string

const (
	// LifetimeWork reclaims the Directory with the Work its owner names.
	LifetimeWork Lifetime = "work"
	// LifetimePermanent keeps the Directory; it is never reclaimed.
	LifetimePermanent Lifetime = "permanent"
)

// Valid reports whether the lifetime is one Storage keeps.
func (l Lifetime) Valid() bool { return l == LifetimeWork || l == LifetimePermanent }

// LocatedDirectory is what applying a Directory Locator to a caller's facts
// gives: the Directory with the backend and lifetime its first allocation
// takes.
type LocatedDirectory struct {
	ID       DirectoryID
	Backend  Backend
	Lifetime Lifetime
}

// NewLocatedDirectory admits a located Directory. A Work lifetime needs an
// owner that names its Work, so reclamation can find it.
// AdmitMapping refuses, before it is opened, a located Directory whose
// backend keeps no Mapped View; the first allocation still decides, so the
// Directory's Map checks again.
func (l LocatedDirectory) AdmitMapping() error {
	if l.Backend != BackendJuiceFS {
		return ErrBackendNotMappable
	}
	return nil
}

func NewLocatedDirectory(id DirectoryID, backend Backend, lifetime Lifetime) (LocatedDirectory, error) {
	if id.Owner.IsZero() || !backend.Valid() || !lifetime.Valid() {
		return LocatedDirectory{}, ErrInvalidLocator
	}
	if _, names := id.Owner.Work(); lifetime == LifetimeWork && !names {
		return LocatedDirectory{}, ErrInvalidLocator
	}
	return LocatedDirectory{ID: id, Backend: backend, Lifetime: lifetime}, nil
}
