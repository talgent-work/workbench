package domain

import (
	"errors"
	"slices"
)

// ErrUnknownAgentHarness refuses a Work naming an Agent Harness Agent does
// not provide.
var ErrUnknownAgentHarness = errors.New("agent: unknown Agent Harness")

// AgentHarness is Agent's immutable description of one native agent program
// a Work runs, including where the program keeps its state: its Harness
// state mount's path beneath $HOME and that Directory's name, and the
// variable that points the program at it. It is reference data that changes
// only with the code launching it; a changed Harness is a new one (Harness
// immutability).
type AgentHarness struct {
	ID string
	// StatePath is the Harness state mount's path relative to $HOME.
	StatePath string
	// StateDirectory names the Harness state Directory, so Works on different
	// Harnesses never share that state (Harness state).
	StateDirectory string
	// StateVariable is the environment variable that points the program at
	// its state mount.
	StateVariable string
}

// ClaudeCode is the one Agent Harness Agent provides, and the default.
var ClaudeCode = AgentHarness{ID: "claude-code", StatePath: ".claude", StateDirectory: "claude-code", StateVariable: "CLAUDE_CONFIG_DIR"}

var agentHarnesses = []AgentHarness{ClaudeCode}

// AgentHarnessByID is the Agent Harness a Work runs: the named one, or the
// default when none is named (Harness choice).
func AgentHarnessByID(id string) (AgentHarness, error) {
	if id == "" {
		return ClaudeCode, nil
	}
	index := slices.IndexFunc(agentHarnesses, func(h AgentHarness) bool { return h.ID == id })
	if index < 0 {
		return AgentHarness{}, ErrUnknownAgentHarness
	}
	return agentHarnesses[index], nil
}

// StateMountPath is the Harness state mount's layout path.
func (h AgentHarness) StateMountPath() string { return HomePath + "/" + h.StatePath }
