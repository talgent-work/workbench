package nativeprocess

import dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"

// AppendCommandInput records an admitted command when its native disposition
// arrives without a user echo (for example, an unknown slash command). Native
// user messages are translated as received; the buffer never rewrites them.
func (s *ProtocolState) AppendCommandInput(workID string, native *Message, messages []*dispatcherpublicv1.UnifiedMessage) []*dispatcherpublicv1.UnifiedMessage {
	if native.ParentToolUseID != "" || native.AgentID != "" {
		return messages
	}
	if !isInputDisposition(native) {
		return messages
	}
	input, admitted := s.Inputs[native.CommandUUID]
	if !admitted || s.Projection.InputTurns[native.CommandUUID].Timestamp != nil || len(s.Projection.PendingInputs[native.CommandUUID]) > 0 {
		return messages
	}
	return append(messages, &dispatcherpublicv1.UnifiedMessage{
		AgentWorkId: workID, Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER,
		Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: inputContent(input.Text)},
	})
}

func inputContent(text string) *dispatcherpublicv1.MessagePayload {
	return &dispatcherpublicv1.MessagePayload{Content: []*dispatcherpublicv1.ContentBlock{{Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT, Text: text}}}
}

func isInputDisposition(native *Message) bool {
	if native.Type != "command_lifecycle" || native.CommandUUID == "" {
		return false
	}
	switch native.CommandState {
	case "started", "completed", "cancelled", "discarded", "refused":
		return true
	default:
		return false
	}
}
