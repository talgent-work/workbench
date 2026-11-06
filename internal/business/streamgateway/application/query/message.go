package query

import "time"

// Message is the product-history read model. Full tool bodies are read through
// GetMessageDetail, independently of the history and live-message window.
type Message struct {
	ID, AgentWorkID, TurnID string
	Timestamp               time.Time
	Agent                   *string
	Kind                    string
	DetailFormat            int32
	DetailLength            uint32
	Text                    *TextMessage
	Action                  *ToolCall
	Observation             *ToolResult
	Permission              *Permission
	Interaction             *Interaction
	Error                   *MessageError
	Result                  *TurnResult
}

type TextMessage struct{ Content []ContentBlock }
type ContentBlock struct {
	Type           int32
	Text, MIMEType string
}
type ToolCall struct{ ToolCallID, ToolName, Summary string }
type ToolResult struct {
	ToolCallID string
	IsError    bool
}
type Permission struct {
	Phase, Decision                          int32
	ID, ToolCallID, ToolName, Prompt, Reason string
	CanAlwaysAllow                           bool
}
type Interaction struct {
	QuestionCount                       uint32
	Phase                               int32
	ID, ToolCallID, ResponseJSON, Error string
}
type MessageError struct{ Code, Message string }
type TurnResult struct {
	Status     int32
	Text       string
	DurationMS int64
}
