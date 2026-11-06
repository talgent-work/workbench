package message

import "encoding/json"

// Product controls retain only fields consumed by their message projection.
// Native protocol transport and reply correlation live in nativeprocess.
type ControlCanUseTool struct {
	ToolName              string            `json:"tool_name"`
	ToolUseID             string            `json:"tool_use_id"`
	Input                 json.RawMessage   `json:"input"`
	Title                 string            `json:"title"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions"`
}
