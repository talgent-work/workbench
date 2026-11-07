package unifiedmessagelog

import (
	"time"

	"github.com/samber/oops"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
)

var messageJSON = protojson.MarshalOptions{UseProtoNames: true}
var messageJSONUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

type messageDO struct {
	ID            string
	AgentWorkID   string
	TurnID        string
	Agent         *string
	Kind          string
	ToolCallID    string
	InteractionID string
	PayloadJSON   string
	// ToolPreviewJSON is the observation's tool output preview; empty when
	// it has none.
	ToolPreviewJSON string
	Timestamp       time.Time
}

func messageToDO(msg *dispatcherpublicv1.UnifiedMessage) (*messageDO, error) {
	if msg == nil {
		return nil, oops.Errorf("dispatcher/unifiedmessagelog: nil message")
	}
	ts := time.Time{}
	if msg.GetTimestamp() != nil {
		ts = msg.GetTimestamp().AsTime().UTC()
	}
	payloadJSON, err := messageJSON.Marshal(&dispatcherpublicv1.UnifiedMessage{
		Payload: msg.GetPayload(),
	})
	if err != nil {
		return nil, oops.Wrap(err)
	}
	do := &messageDO{
		ID:          msg.GetId(),
		AgentWorkID: msg.GetAgentWorkId(),
		TurnID:      msg.GetTurnId(), Agent: msg.Agent,
		Kind:        msg.GetKind().String(),
		PayloadJSON: string(payloadJSON),
		Timestamp:   ts,
	}
	if preview := msg.GetToolPreview(); preview != nil {
		previewJSON, err := messageJSON.Marshal(preview)
		if err != nil {
			return nil, oops.Wrap(err)
		}
		do.ToolPreviewJSON = string(previewJSON)
	}
	extractIndexes(do, msg)
	return do, nil
}

func doToMessage(do *messageDO) (*dispatcherpublicv1.UnifiedMessage, error) {
	if do == nil {
		return nil, oops.Errorf("dispatcher/unifiedmessagelog: nil do")
	}
	msg := &dispatcherpublicv1.UnifiedMessage{}
	if do.PayloadJSON != "" {
		if err := messageJSONUnmarshal.Unmarshal([]byte(do.PayloadJSON), msg); err != nil {
			return nil, oops.Wrap(err)
		}
	}
	msg.Id = do.ID
	msg.AgentWorkId = do.AgentWorkID
	msg.TurnId, msg.Agent = do.TurnID, do.Agent
	msg.Timestamp = timestamppb.New(do.Timestamp)
	if msg.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED {
		msg.Kind = kindFromString(do.Kind)
	}
	return msg, nil
}

func extractIndexes(do *messageDO, msg *dispatcherpublicv1.UnifiedMessage) {
	switch payload := msg.GetPayload().(type) {
	case *dispatcherpublicv1.UnifiedMessage_Action:
		do.ToolCallID = payload.Action.GetToolCallId()
	case *dispatcherpublicv1.UnifiedMessage_Observation:
		do.ToolCallID = payload.Observation.GetToolCallId()
	case *dispatcherpublicv1.UnifiedMessage_Permission:
		do.ToolCallID = payload.Permission.GetToolCallId()
		do.InteractionID = payload.Permission.GetPermissionId()
	case *dispatcherpublicv1.UnifiedMessage_Interaction:
		do.ToolCallID = payload.Interaction.GetToolCallId()
		do.InteractionID = payload.Interaction.GetInteractionId()
	}
}

func kindFromString(s string) dispatcherpublicv1.UnifiedMessageKind {
	if v, ok := dispatcherpublicv1.UnifiedMessageKind_value[s]; ok {
		return dispatcherpublicv1.UnifiedMessageKind(v)
	}
	return dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED
}
