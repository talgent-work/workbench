package claudecode

import (
	"encoding/json"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode/message"
)

func fromControlRequest(sessionID string, raw []byte) (*dispatcherpublicv1.UnifiedMessage, error) {
	var env struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	var subEnv struct {
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(env.Request, &subEnv)

	switch subEnv.Subtype {
	case "can_use_tool":
		var c message.ControlCanUseTool
		_ = json.Unmarshal(env.Request, &c)
		if c.ToolName == "AskUserQuestion" {
			return newInteractionMsg(sessionID, env.Type, subEnv.Subtype, raw, &dispatcherpublicv1.InteractionPayload{
				Phase:         dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_REQUESTED,
				InteractionId: env.RequestID, ToolCallId: c.ToolUseID,
				QuestionCount: questionCount(c.Input),
			}), nil
		}
		return newPermissionMsg(sessionID, env.Type, subEnv.Subtype, raw, &dispatcherpublicv1.PermissionPayload{
			Phase:        dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_REQUESTED,
			PermissionId: env.RequestID, ToolCallId: c.ToolUseID,
			ToolName: c.ToolName, Prompt: c.Title,
			CanAlwaysAllow: len(c.PermissionSuggestions) > 0,
		}), nil
	}
	return nil, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ControlOutcomeToMessage correlates the native acknowledgement with its request.
// Questions reuse the original tool input; only the submitted answers are added.
func ControlOutcomeToMessage(sessionID string, raw []byte, toolName, toolCallID string, input json.RawMessage) (*dispatcherpublicv1.UnifiedMessage, error) {
	var env struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Response  struct {
			RequestID string `json:"request_id"`
			Subtype   string `json:"subtype"`
			Error     string `json:"error"`
			Message   string `json:"message"`
			Response  struct {
				Behavior     string `json:"behavior"`
				Message      string `json:"message"`
				UpdatedInput struct {
					Answers json.RawMessage `json:"answers"`
				} `json:"updatedInput"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	cancelled, failed := env.Type == "control_cancel_request", env.Response.Subtype == "error"
	id, behavior := env.Response.RequestID, env.Response.Response.Behavior
	if cancelled {
		id = env.RequestID
	}
	if id == "" || (!cancelled && !failed && (env.Response.Subtype != "success" || (behavior != "allow" && behavior != "deny"))) {
		return nil, nil
	}
	failure := firstNonEmpty(env.Response.Error, env.Response.Message)
	if toolName != "AskUserQuestion" {
		phase, decision := dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_DECIDED, dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_ALLOW
		reason := env.Response.Response.Message
		switch {
		case cancelled:
			phase, decision = dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_CANCELLED, dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_CANCEL
		case failed:
			phase, decision, reason = dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_FAILED, dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_UNSPECIFIED, failure
		case behavior == "deny":
			decision = dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_DENY
		}
		return newPermissionMsg(sessionID, env.Type, "can_use_tool", raw, &dispatcherpublicv1.PermissionPayload{
			Phase: phase, PermissionId: id, ToolName: toolName, ToolCallId: toolCallID, Decision: decision, Reason: reason,
		}), nil
	}
	phase := dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_RESPONDED
	switch {
	case cancelled:
		phase = dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_CANCELLED
	case failed:
		phase = dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_FAILED
	case behavior == "deny":
		phase = dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_SKIPPED
	}
	return newInteractionMsg(sessionID, env.Type, "can_use_tool", raw, &dispatcherpublicv1.InteractionPayload{
		Phase:         phase,
		InteractionId: id, ToolCallId: toolCallID, ResponseJson: string(env.Response.Response.UpdatedInput.Answers), Error: failure,
		QuestionCount: questionCount(input),
	}), nil
}

func questionCount(input json.RawMessage) uint32 {
	var value struct {
		Questions []json.RawMessage `json:"questions"`
	}
	if json.Unmarshal(input, &value) != nil {
		return 0
	}
	return uint32(len(value.Questions))
}
