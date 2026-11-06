package domain

import (
	"fmt"
	"regexp"
)

// ThinkingLevels is the Agent Harness's effort vocabulary. A Model Template
// offers the subset its vendor serves, the machine's Claude login all of them;
// a Work retains one of them.
var ThinkingLevels = []string{"low", "medium", "high", "xhigh", "max"}

// WorkModelSelection is what the Work's conversation uses for its model,
// retained for its life: a Model Configuration, by its id, or, on a Managed
// Computer, the machine's own Claude login. Exactly one is selected. Each start
// with a Model Configuration uses the Model Template it names at that start.
// Thinking level belongs to the Work separately and is not part of this value.
type WorkModelSelection struct {
	ConfigurationID string `validate:"max=128"`
	// MachineLogin selects the Managed Computer's own Claude login; the Work
	// then has no Model Configuration.
	MachineLogin bool
}

// MachineLoginModel selects the Managed Computer's own Claude login.
func MachineLoginModel() WorkModelSelection { return WorkModelSelection{MachineLogin: true} }

// A Model Configuration id is lowercase letters, digits, '.', '-' and '_'.
var modelConfigurationID = regexp.MustCompile(`^[a-z0-9._-]+$`)

func (m WorkModelSelection) Validate() error {
	if m.MachineLogin {
		if m.ConfigurationID != "" {
			return fmt.Errorf("%w: a Work model selection names either a Model Configuration or the machine's Claude login", ErrInvalidWork)
		}
		return nil
	}
	if err := validation.Struct(m); err != nil || !modelConfigurationID.MatchString(m.ConfigurationID) {
		return fmt.Errorf("%w: invalid Work model selection", ErrInvalidWork)
	}
	return nil
}

// Available reports whether the selection admits a start, given whether its
// Model Configuration is currently provided. The machine's Claude login has
// no Model Configuration to withdraw.
func (m WorkModelSelection) Available(configurationProvided bool) bool {
	return m.MachineLogin || configurationProvided
}

var ErrMachineLoginNotManaged = fmt.Errorf("%w: the machine's Claude login is available only on a Managed Computer", ErrInvalidWork)

// AdmitOn decides whether the selection may be retained by a Work on its
// Computer: the machine's Claude login only on a Managed Computer, a Model
// Configuration on any Computer. Both are fixed at the Work's first start, so
// this is decided once, when the Work is established.
func (m WorkModelSelection) AdmitOn(managed bool) error {
	if m.MachineLogin && !managed {
		return ErrMachineLoginNotManaged
	}
	return nil
}
