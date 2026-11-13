package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalidLocator means a Directory Locator names an unknown or repeated
// condition, an invalid name, backend or lifetime, or a Work's lifetime
// without following the Work.
var ErrInvalidLocator = errors.New("agent: invalid Directory Locator")

// LocatorCondition names a fact a Directory follows, in the vocabulary every
// context applying a Locator shares, so the same Locator applied to the same
// facts reaches the same Directory wherever it is applied.
type LocatorCondition string

// The facts a Work Layout's Locators follow (Locator facts).
const (
	// ConditionWork is the Work's identity.
	ConditionWork LocatorCondition = "work"
	// ConditionFileHome is the Work's File Home as "<kind>:<id>".
	ConditionFileHome LocatorCondition = "file_home"
	// ConditionPerson is the person the Work's Agent represents.
	ConditionPerson LocatorCondition = "person"
	// ConditionStarter is the person who started the Work.
	ConditionStarter LocatorCondition = "starter"
	// ConditionWorkCaller is the Work's Work Caller as "<kind>:<id>".
	ConditionWorkCaller LocatorCondition = "work_caller"
	// ConditionAgentDefinition is the Work's AgentDefinition.
	ConditionAgentDefinition LocatorCondition = "agent_definition"
)

var locatorConditions = []LocatorCondition{ConditionWork, ConditionFileHome, ConditionPerson, ConditionStarter, ConditionWorkCaller, ConditionAgentDefinition}

// DirectoryBackend is where a Directory's files live, fixed by Storage at
// its first allocation.
type DirectoryBackend string

const (
	BackendNAS     DirectoryBackend = "nas"
	BackendJuiceFS DirectoryBackend = "juicefs"
)

// DirectoryLifetime is whether a Directory is reclaimed with the Work its
// owner names or kept permanently.
type DirectoryLifetime string

const (
	LifetimeWork      DirectoryLifetime = "work"
	LifetimePermanent DirectoryLifetime = "permanent"
)

// DirectoryLocator is Storage's Directory Locator as Agent applies it: the
// facts a Directory follows, its name, backend and lifetime.
type DirectoryLocator struct {
	Conditions []LocatorCondition
	Name       string
	Backend    DirectoryBackend
	Lifetime   DirectoryLifetime
}

const maxDirectoryNameLength = 128

// Validate admits known conditions, each once, a name of one path element,
// a known backend and lifetime, and a Work's lifetime only for a Directory
// that follows the Work, which Storage would refuse.
func (l DirectoryLocator) Validate() error {
	if len(l.Conditions) == 0 {
		return fmt.Errorf("%w: a Locator follows at least one fact", ErrInvalidLocator)
	}
	for i, condition := range l.Conditions {
		if !slices.Contains(locatorConditions, condition) || slices.Contains(l.Conditions[:i], condition) {
			return fmt.Errorf("%w: condition %q", ErrInvalidLocator, condition)
		}
	}
	if err := validateDirectoryName(l.Name); err != nil {
		return err
	}
	return l.validateKind()
}

func (l DirectoryLocator) validateKind() error {
	if l.Backend != BackendNAS && l.Backend != BackendJuiceFS {
		return fmt.Errorf("%w: backend %q", ErrInvalidLocator, l.Backend)
	}
	switch l.Lifetime {
	case LifetimePermanent:
	case LifetimeWork:
		if !l.follows(ConditionWork) {
			return fmt.Errorf("%w: a Work's lifetime needs the Work's fact", ErrInvalidLocator)
		}
	default:
		return fmt.Errorf("%w: lifetime %q", ErrInvalidLocator, l.Lifetime)
	}
	return nil
}

func validateDirectoryName(name string) error {
	if name == "" || len(name) > maxDirectoryNameLength || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\\x00") || strings.TrimSpace(name) != name {
		return fmt.Errorf("%w: name %q", ErrInvalidLocator, name)
	}
	return nil
}

func (l DirectoryLocator) follows(condition LocatorCondition) bool {
	return slices.Contains(l.Conditions, condition)
}

func (l DirectoryLocator) clone() DirectoryLocator {
	l.Conditions = slices.Clone(l.Conditions)
	return l
}

// LocatorFacts are a Work's values of the Locator conditions; a fact the
// Work lacks is absent.
type LocatorFacts map[LocatorCondition]string

// OwnerFact is one condition of a Directory owner with its value.
type OwnerFact struct {
	Condition LocatorCondition
	Value     string
}

// Directory is a Directory's identity: its owner, facts ordered by condition
// name, and its name.
type Directory struct {
	Owner []OwnerFact
	Name  string
}

// Equal reports whether both name the same Directory.
func (d Directory) Equal(other Directory) bool {
	return d.Name == other.Name && slices.Equal(d.Owner, other.Owner)
}

func (d Directory) clone() Directory {
	d.Owner = slices.Clone(d.Owner)
	return d
}

// Present reports whether a Directory is named.
func (d Directory) Present() bool { return d.Name != "" }

// LocatedDirectory is what applying a Locator to facts gives: the Directory
// with the backend and lifetime its first allocation takes.
type LocatedDirectory struct {
	Directory Directory
	Backend   DirectoryBackend
	Lifetime  DirectoryLifetime
}

func (d LocatedDirectory) clone() LocatedDirectory {
	d.Directory = d.Directory.clone()
	return d
}

// Apply locates the Directory for the facts: with a value for each of its
// conditions it gives one owner and name; lacking one it locates nothing
// (Missing fact).
func (l DirectoryLocator) Apply(facts LocatorFacts) (LocatedDirectory, bool) {
	owner := make([]OwnerFact, 0, len(l.Conditions))
	for _, condition := range l.Conditions {
		value := facts[condition]
		if value == "" {
			return LocatedDirectory{}, false
		}
		owner = append(owner, OwnerFact{Condition: condition, Value: value})
	}
	slices.SortFunc(owner, func(a, b OwnerFact) int { return strings.Compare(string(a.Condition), string(b.Condition)) })
	return LocatedDirectory{Directory: Directory{Owner: owner, Name: l.Name}, Backend: l.Backend, Lifetime: l.Lifetime}, true
}

// validate admits a located Directory as its Locator could give it.
func (d LocatedDirectory) validate() error {
	if len(d.Directory.Owner) == 0 {
		return fmt.Errorf("%w: a located Directory has an owner", ErrInvalidWork)
	}
	locator := DirectoryLocator{Name: d.Directory.Name, Backend: d.Backend, Lifetime: d.Lifetime}
	for i, fact := range d.Directory.Owner {
		if fact.Value == "" || (i > 0 && d.Directory.Owner[i-1].Condition >= fact.Condition) {
			return fmt.Errorf("%w: invalid Directory owner", ErrInvalidWork)
		}
		locator.Conditions = append(locator.Conditions, fact.Condition)
	}
	if err := locator.Validate(); err != nil {
		return errors.Join(ErrInvalidWork, err)
	}
	return nil
}
