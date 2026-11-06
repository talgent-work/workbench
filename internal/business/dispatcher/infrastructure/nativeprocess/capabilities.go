package nativeprocess

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
)

// ObserveCapabilities consumes complete native inventories. It does not infer
// callable candidates from package declarations, tool lists or Skill bodies.
func (s *ProtocolState) ObserveCapabilities(message *Message, initializeID string) bool {
	if message == nil || message.ParentToolUseID != "" || message.AgentID != "" {
		return false
	}
	var raw []byte
	switch {
	case message.Type == "control_response" && initializeID != "" && message.Response.RequestID == initializeID && message.Response.Subtype == "success":
		raw = message.Response.Response.Commands
	case message.Type == "system" && message.Subtype == "commands_changed":
		raw = message.Commands
	default:
		return false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var commands []struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		ArgumentHint string `json:"argumentHint"`
	}
	if json.Unmarshal(raw, &commands) != nil || len(commands) > 4096 {
		return false
	}
	result := make([]application.NativeCommand, 0, len(commands))
	seen := make(map[string]bool, len(commands))
	for _, item := range commands {
		if strings.TrimSpace(item.Name) == "" || len(item.Name) > 512 || seen[item.Name] {
			return false
		}
		seen[item.Name] = true
		result = append(result, application.NativeCommand{Name: item.Name, Description: item.Description, ArgumentHint: item.ArgumentHint})
	}
	s.Capabilities = application.Capabilities{Observed: true, Commands: result}
	return true
}
