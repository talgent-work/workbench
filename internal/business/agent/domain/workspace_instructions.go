package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxWorkspaceInstructionsRunes bounds Workspace Instructions at about the
// 200 lines the Agent Harness recommends for one instructions file. Each start
// carries them inline into its environment's preparation, whose command
// stays within one process argument.
const MaxWorkspaceInstructionsRunes = 16000

var (
	// ErrWorkspaceInstructionsForbidden refuses reading or writing Workspace
	// Instructions through the definition to anyone but one of its Owners or
	// an Application's Agent's Application Owner.
	ErrWorkspaceInstructionsForbidden = errors.New("agent: only an Owner of the Agent reads or writes its Workspace Instructions")
	// ErrInvalidWorkspaceInstructions refuses Workspace Instructions longer
	// than MaxWorkspaceInstructionsRunes.
	ErrInvalidWorkspaceInstructions = fmt.Errorf("agent: Workspace Instructions are at most %d characters", MaxWorkspaceInstructionsRunes)
)

// ReadWorkspaceInstructions returns the Workspace Instructions to one of the
// definition's Owners, or to an Application's Agent's Application Owner. A
// Work's readers read the copy its Session Workspace holds instead.
func (a *AgentDefinition) ReadWorkspaceInstructions(requester DefinitionRequester) (string, error) {
	if !a.admitsConfiguration(requester) {
		return "", ErrWorkspaceInstructionsForbidden
	}
	return a.WorkspaceInstructions, nil
}

// ChangeWorkspaceInstructions replaces the Workspace Instructions when the
// requester may configure the definition. Running Turns keep what their
// start placed; each Work places the new ones at its next start.
func (a *AgentDefinition) ChangeWorkspaceInstructions(requester DefinitionRequester, instructions string) (bool, error) {
	if !a.admitsConfiguration(requester) {
		return false, ErrWorkspaceInstructionsForbidden
	}
	instructions = strings.TrimSpace(instructions)
	if err := validateWorkspaceInstructions(instructions); err != nil {
		return false, err
	}
	if instructions == a.WorkspaceInstructions {
		return false, nil
	}
	a.WorkspaceInstructions = instructions
	return true, nil
}

func validateWorkspaceInstructions(instructions string) error {
	if utf8.RuneCountInString(instructions) > MaxWorkspaceInstructionsRunes {
		return ErrInvalidWorkspaceInstructions
	}
	return nil
}
