package connectrpc

import (
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func messageToProto(m *query.Message) *dispatcherpublicv1.UnifiedMessage {
	if m == nil {
		return nil
	}
	out := &dispatcherpublicv1.UnifiedMessage{Id: m.ID, AgentWorkId: m.AgentWorkID, TurnId: m.TurnID, Agent: m.Agent, Kind: dispatcherpublicv1.UnifiedMessageKind(dispatcherpublicv1.UnifiedMessageKind_value[m.Kind]), DetailFormat: dispatcherpublicv1.MessageDetailFormat(m.DetailFormat), DetailLength: m.DetailLength}
	if !m.Timestamp.IsZero() {
		out.Timestamp = timestamppb.New(m.Timestamp)
	}
	switch {
	case m.Text != nil:
		text := &dispatcherpublicv1.MessagePayload{}
		for _, block := range m.Text.Content {
			text.Content = append(text.Content, &dispatcherpublicv1.ContentBlock{Type: dispatcherpublicv1.ContentBlockType(block.Type), Text: block.Text, MimeType: block.MIMEType})
		}
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Message{Message: text}
	case m.Action != nil:
		v := m.Action
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Action{Action: &dispatcherpublicv1.ActionPayload{ToolCallId: v.ToolCallID, ToolName: v.ToolName, Summary: v.Summary}}
	case m.Observation != nil:
		v := m.Observation
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Observation{Observation: &dispatcherpublicv1.ObservationPayload{ToolCallId: v.ToolCallID, IsError: v.IsError}}
	case m.Permission != nil:
		v := m.Permission
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Permission{Permission: &dispatcherpublicv1.PermissionPayload{Phase: dispatcherpublicv1.PermissionPhase(v.Phase), PermissionId: v.ID, ToolCallId: v.ToolCallID, ToolName: v.ToolName, Prompt: v.Prompt, Decision: dispatcherpublicv1.PermissionDecision(v.Decision), Reason: v.Reason, CanAlwaysAllow: v.CanAlwaysAllow}}
	case m.Interaction != nil:
		v := m.Interaction
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Interaction{Interaction: &dispatcherpublicv1.InteractionPayload{Phase: dispatcherpublicv1.InteractionPhase(v.Phase), InteractionId: v.ID, ToolCallId: v.ToolCallID, ResponseJson: v.ResponseJSON, QuestionCount: v.QuestionCount, Error: v.Error}}
	case m.Error != nil:
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Error{Error: &dispatcherpublicv1.ErrorPayload{Code: m.Error.Code, Message: m.Error.Message}}
	case m.Result != nil:
		v := m.Result
		out.Payload = &dispatcherpublicv1.UnifiedMessage_Result{Result: &dispatcherpublicv1.ResultPayload{Status: dispatcherpublicv1.ResultStatus(v.Status), Text: v.Text, DurationMs: v.DurationMS}}
	}
	return out
}
