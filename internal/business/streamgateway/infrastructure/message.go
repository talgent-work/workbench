package infrastructure

import (
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

// MessageFromProto maps the published Dispatcher contract to the local read model.
func MessageFromProto(m *dispatcherpublicv1.UnifiedMessage) *query.Message {
	if m == nil {
		return nil
	}
	out := &query.Message{ID: m.Id, AgentWorkID: m.AgentWorkId, TurnID: m.TurnId, Agent: m.Agent, Kind: m.Kind.String(), DetailFormat: int32(m.DetailFormat), DetailLength: m.DetailLength}
	if m.Timestamp != nil {
		out.Timestamp = m.Timestamp.AsTime()
	}
	switch p := m.Payload.(type) {
	case *dispatcherpublicv1.UnifiedMessage_Message:
		out.Text = &query.TextMessage{}
		for _, block := range p.Message.GetContent() {
			out.Text.Content = append(out.Text.Content, query.ContentBlock{Type: int32(block.GetType()), Text: block.GetText(), MIMEType: block.GetMimeType()})
		}
	case *dispatcherpublicv1.UnifiedMessage_Action:
		v := p.Action
		out.Action = &query.ToolCall{ToolCallID: v.GetToolCallId(), ToolName: v.GetToolName(), Summary: v.GetSummary()}
	case *dispatcherpublicv1.UnifiedMessage_Observation:
		v := p.Observation
		out.Observation = &query.ToolResult{ToolCallID: v.GetToolCallId(), IsError: v.GetIsError()}
	case *dispatcherpublicv1.UnifiedMessage_Permission:
		v := p.Permission
		out.Permission = &query.Permission{Phase: int32(v.GetPhase()), Decision: int32(v.GetDecision()), ID: v.GetPermissionId(), ToolCallID: v.GetToolCallId(), ToolName: v.GetToolName(), Prompt: v.GetPrompt(), Reason: v.GetReason(), CanAlwaysAllow: v.GetCanAlwaysAllow()}
	case *dispatcherpublicv1.UnifiedMessage_Interaction:
		v := p.Interaction
		out.Interaction = &query.Interaction{Phase: int32(v.GetPhase()), ID: v.GetInteractionId(), ToolCallID: v.GetToolCallId(), ResponseJSON: v.GetResponseJson(), QuestionCount: v.GetQuestionCount(), Error: v.GetError()}
	case *dispatcherpublicv1.UnifiedMessage_Error:
		out.Error = &query.MessageError{Code: p.Error.GetCode(), Message: p.Error.GetMessage()}
	case *dispatcherpublicv1.UnifiedMessage_Result:
		v := p.Result
		out.Result = &query.TurnResult{Status: int32(v.GetStatus()), Text: v.GetText(), DurationMS: v.GetDurationMs()}
	}
	return out
}
