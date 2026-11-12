package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxSoulRunes bounds a Soul; it shapes style and never carries documents.
const MaxSoulRunes = 8000

var (
	// ErrSoulForbidden refuses reading or writing a Soul to anyone but one of
	// the definition's Owners or an Application's Agent's Application Owner.
	ErrSoulForbidden = errors.New("agent: only an Owner of the Agent reads or writes its Soul")
	// ErrInvalidSoul refuses a Soul longer than MaxSoulRunes.
	ErrInvalidSoul = fmt.Errorf("agent: a Soul is at most %d characters", MaxSoulRunes)
)

// ReadSoul returns the Soul to one of the definition's Owners, or to an
// Application's Agent's Application Owner (Soul authorship, Application Owner
// configuration).
func (a *AgentDefinition) ReadSoul(requester DefinitionRequester) (string, error) {
	if !a.admitsConfiguration(requester) {
		return "", ErrSoulForbidden
	}
	return a.Soul, nil
}

// ChangeSoul replaces the Soul when the requester is one of the definition's
// Owners. Running Turns keep the Soul their execution took up; each Work
// takes up the new one at its next start.
func (a *AgentDefinition) ChangeSoul(requester DefinitionRequester, soul string) (bool, error) {
	if !a.admitsConfiguration(requester) {
		return false, ErrSoulForbidden
	}
	soul = strings.TrimSpace(soul)
	if err := validateSoul(soul); err != nil {
		return false, err
	}
	if soul == a.Soul {
		return false, nil
	}
	a.Soul = soul
	return true, nil
}

func validateSoul(soul string) error {
	if utf8.RuneCountInString(soul) > MaxSoulRunes {
		return ErrInvalidSoul
	}
	return nil
}
