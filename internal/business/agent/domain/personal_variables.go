package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ReservedVariablePrefix starts the names of the platform's own variables,
// which no Personal Variable takes. It is matched regardless of case: a
// Managed Computer may run Windows, whose environment names ignore case, and
// the Harness launch keeps its own shell variables under the lowercase form.
const ReservedVariablePrefix = "TALGENT_"

var (
	// ErrPersonalVariableForbidden refuses reading or changing a person's
	// Personal Variables to anyone but that person.
	ErrPersonalVariableForbidden = errors.New("agent: only its person reads or changes a Personal Variable")
	// ErrPersonalVariableNotFound answers a read of a name the person does
	// not keep.
	ErrPersonalVariableNotFound = errors.New("agent: no Personal Variable of that name")
	// ErrPersonalVariableSetNotFound answers a person who keeps no Personal
	// Variables yet.
	ErrPersonalVariableSetNotFound = errors.New("agent: the person keeps no Personal Variables")
	// ErrInvalidPersonalVariable refuses a Personal Variable that breaks one
	// of its rules; each rule's error wraps it and names the rule, never the
	// value.
	ErrInvalidPersonalVariable = errors.New("agent: invalid Personal Variable")
	// ErrInvalidPersonalVariableName refuses a name that is not a POSIX
	// environment variable name.
	ErrInvalidPersonalVariableName = fmt.Errorf("%w: a name is a letter or underscore followed by letters, digits or underscores", ErrInvalidPersonalVariable)
	// ErrReservedPersonalVariableName refuses a name the platform reserves.
	ErrReservedPersonalVariableName = fmt.Errorf("%w: names starting with %s are reserved for the platform", ErrInvalidPersonalVariable, ReservedVariablePrefix)
	// ErrInvalidPersonalVariableValue refuses a value an environment cannot
	// carry.
	ErrInvalidPersonalVariableValue = fmt.Errorf("%w: a value contains no NUL character", ErrInvalidPersonalVariable)
)

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// PersonalVariable is one named value a person keeps for the Works their
// Agent does for them.
type PersonalVariable struct {
	Name  string
	Value string
}

// Validate applies the rules every Personal Variable keeps: a POSIX
// environment variable name outside the platform's reserved prefix, and a
// value without NUL, possibly multi-line.
func (v PersonalVariable) Validate() error {
	if !variableName.MatchString(v.Name) {
		return ErrInvalidPersonalVariableName
	}
	if ReservedVariableName(v.Name) {
		return ErrReservedPersonalVariableName
	}
	if strings.ContainsRune(v.Value, 0) {
		return ErrInvalidPersonalVariableValue
	}
	return nil
}

// ReservedVariableName reports whether name starts with the platform's
// reserved prefix, in any case.
func ReservedVariableName(name string) bool {
	return len(name) >= len(ReservedVariablePrefix) && strings.EqualFold(name[:len(ReservedVariablePrefix)], ReservedVariablePrefix)
}

// PersonalVariableSet is one person's Personal Variables, which the person's
// Works enable at creation and receive at every start. Only the person reads or changes it.
type PersonalVariableSet struct {
	PersonID string
	// Variables hold one value for each name, ordered by name.
	Variables []PersonalVariable
	Version   int
}

// NewPersonalVariableSet starts the empty set of a person who keeps no
// Personal Variables yet.
func NewPersonalVariableSet(personID string) (*PersonalVariableSet, error) {
	set := &PersonalVariableSet{PersonID: strings.TrimSpace(personID)}
	return set, set.Validate()
}

// Validate checks the set as persisted: a person, valid variables and one
// value for each name in name order.
func (s *PersonalVariableSet) Validate() error {
	if s.PersonID == "" || s.Version < 0 {
		return ErrInvalidPersonalVariable
	}
	for i, variable := range s.Variables {
		if err := variable.Validate(); err != nil {
			return err
		}
		if i > 0 && s.Variables[i-1].Name >= variable.Name {
			return ErrInvalidPersonalVariable
		}
	}
	return nil
}

// SetVariable sets a Personal Variable for its person, replacing any value of
// that name (Personal Variable authority, One value per name, Variable names,
// Reserved names). Setting the value it holds changes nothing.
func (s *PersonalVariableSet) SetVariable(requester string, variable PersonalVariable) (bool, error) {
	if err := s.admit(requester); err != nil {
		return false, err
	}
	if err := variable.Validate(); err != nil {
		return false, err
	}
	i, found := s.find(variable.Name)
	switch {
	case found && s.Variables[i].Value == variable.Value:
		return false, nil
	case found:
		s.Variables[i].Value = variable.Value
	default:
		s.Variables = slices.Insert(s.Variables, i, variable)
	}
	return true, nil
}

// RemoveVariable removes its person's Personal Variable of a name (Personal
// Variable authority); removing a name it does not hold changes nothing.
func (s *PersonalVariableSet) RemoveVariable(requester, name string) (bool, error) {
	if err := s.admit(requester); err != nil {
		return false, err
	}
	i, found := s.find(name)
	if !found {
		return false, nil
	}
	s.Variables = slices.Delete(s.Variables, i, i+1)
	return true, nil
}

// Names lists its person's Personal Variables by name, in name order, without
// values (Personal Variable authority).
func (s *PersonalVariableSet) Names(requester string) ([]string, error) {
	if err := s.admit(requester); err != nil {
		return nil, err
	}
	return s.OfferedNames(), nil
}

// Reveal reads one of its person's values for that person (Personal Variable
// authority).
func (s *PersonalVariableSet) Reveal(requester, name string) (string, error) {
	if err := s.admit(requester); err != nil {
		return "", err
	}
	i, found := s.find(name)
	if !found {
		return "", ErrPersonalVariableNotFound
	}
	return s.Variables[i].Value, nil
}

// OfferedNames are the names of every Personal Variable it holds, in name
// order, which a new Work of its person is offered as enabled (Variable
// selection at creation).
func (s *PersonalVariableSet) OfferedNames() []string {
	names := make([]string, len(s.Variables))
	for i, variable := range s.Variables {
		names[i] = variable.Name
	}
	return names
}

// CurrentValues are the values a start of one of its person's Works takes now
// (Personal Variables at start): those of the names its Work Variable
// Selection names, skipping a name it no longer holds (Removed variable).
func (s *PersonalVariableSet) CurrentValues(selection WorkVariableSelection) []PersonalVariable {
	var values []PersonalVariable
	for _, name := range selection.Names {
		if i, found := s.find(name); found {
			values = append(values, s.Variables[i])
		}
	}
	return values
}

func (s *PersonalVariableSet) admit(requester string) error {
	if strings.TrimSpace(requester) == "" || requester != s.PersonID {
		return ErrPersonalVariableForbidden
	}
	return nil
}

func (s *PersonalVariableSet) find(name string) (int, bool) {
	return slices.BinarySearchFunc(s.Variables, name, func(v PersonalVariable, name string) int { return strings.Compare(v.Name, name) })
}
