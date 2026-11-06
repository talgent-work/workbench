// Package claudecode translates Claude SDKMessage and control-protocol frames to UnifiedMessage.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

// UpstreamToMessages translates one native frame into Work Message Stream
// history and, separately, a live notice that is never history. ctx is
// currently unused; kept for future logging / cancellation hooks.
func UpstreamToMessages(_ context.Context, sessionID string, raw []byte) ([]*dispatcherpublicv1.UnifiedMessage, *adapter.Notice, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, fmt.Errorf("claudecode: unmarshal envelope: %w", err)
	}
	switch env.Type {
	case "assistant":
		return fromAssistant(sessionID, &env, raw)
	case "system":
		notice, err := fromSystem(&env, raw)
		return nil, notice, err
	}
	messages, err := historyMessages(sessionID, &env, raw)
	return messages, nil, err
}

func historyMessages(sessionID string, env *envelope, raw []byte) ([]*dispatcherpublicv1.UnifiedMessage, error) {
	switch env.Type {
	case "user":
		return fromUser(sessionID, env, raw)
	case "result":
		return fromResult(sessionID, raw)
	case "auth_status":
		// Harness credential acquisition, not a model provider refusal: kept as
		// history. Revisit if an apiKeyHelper makes it precede a failed Result.
		return single(fromAuthStatus(sessionID, raw))
	case "rate_limit_event":
		// A rejected usage limit lasts until it resets and is not recovered
		// automatically; the Turn's failed Result explains it.
		return nil, nil
	case "control_request":
		return single(fromControlRequest(sessionID, raw))
	case "control_response":
		// Only ControlOutcomeToMessage projects replies correlated by native state.
		return nil, nil
	}
	// stream_event, tool_progress, control_cancel_request and unknown frames.
	return nil, nil
}

func single(msg *dispatcherpublicv1.UnifiedMessage, err error) ([]*dispatcherpublicv1.UnifiedMessage, error) {
	if err != nil || msg == nil {
		return nil, err
	}
	return []*dispatcherpublicv1.UnifiedMessage{msg}, nil
}

// envelope is the minimal shared shape across all Claude frames. Only fields the entrypoint
// or helpers peek at directly are here; full subtype fields land via a second Unmarshal.
type envelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

func newBaseMsg(sessionID, runtimeType, runtimeSubtype string, raw []byte) *dispatcherpublicv1.UnifiedMessage {
	return &dispatcherpublicv1.UnifiedMessage{AgentWorkId: sessionID}
}

func newMessageMsg(sessionID string, runtimeType, runtimeSubtype string, raw []byte, payload *dispatcherpublicv1.MessagePayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, runtimeType, runtimeSubtype, raw)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT
	if runtimeType == "user" {
		m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER
	}
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Message{Message: payload}
	return m
}

func newResultMsg(sessionID string, runtimeType, runtimeSubtype string, raw []byte, payload *dispatcherpublicv1.ResultPayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, runtimeType, runtimeSubtype, raw)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Result{Result: payload}
	return m
}

func newObservationMsg(sessionID string, runtimeType, runtimeSubtype string, raw []byte, payload *dispatcherpublicv1.ObservationPayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, runtimeType, runtimeSubtype, raw)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Observation{Observation: payload}
	return m
}

func newPermissionMsg(sessionID string, runtimeType, runtimeSubtype string, raw []byte, payload *dispatcherpublicv1.PermissionPayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, runtimeType, runtimeSubtype, raw)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_PERMISSION
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Permission{Permission: payload}
	return m
}

func newActionMsg(sessionID string, runtimeType, runtimeSubtype string, raw []byte, payload *dispatcherpublicv1.ActionPayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, runtimeType, runtimeSubtype, raw)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Action{Action: payload}
	return m
}

func newInteractionMsg(sessionID string, runtimeType, runtimeSubtype string, raw []byte, payload *dispatcherpublicv1.InteractionPayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, runtimeType, runtimeSubtype, raw)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_INTERACTION
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Interaction{Interaction: payload}
	return m
}

// errorMessage keeps original native error text, with long bodies fetched on demand.
func errorMessage(sessionID string, payload *dispatcherpublicv1.ErrorPayload) *dispatcherpublicv1.UnifiedMessage {
	m := newBaseMsg(sessionID, "", "", nil)
	m.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ERROR
	m.Payload = &dispatcherpublicv1.UnifiedMessage_Error{Error: payload}
	count := 0
	for i := range payload.Message {
		if count == 1024 {
			original := payload.Message
			payload.Message = strings.Clone(original[:i])
			return withDetail(m, original, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT)
		}
		count++
	}
	return m
}
