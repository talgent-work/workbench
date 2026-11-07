package nativeprocess

import (
	"crypto/sha1"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MessageProjection retains only the protocol correlations still needed to
// attribute UnifiedMessages; settled ones are dropped. It is part of the
// stream's state, not Work state.
type MessageProjection struct {
	CurrentTurnID string                          `json:"current_turn_id,omitempty"`
	InputTurns    map[string]inputTurn            `json:"input_turns,omitempty"`
	PendingInputs map[string][]pendingUserMessage `json:"pending_inputs,omitempty"`
	Tools         map[string]MessageAttribution   `json:"tools,omitempty"`
	Agents        map[string]MessageAttribution   `json:"agents,omitempty"`
	Requests      map[string]MessageAttribution   `json:"requests,omitempty"`
}

// A known input can have no turn when cancelled before starting. Retain its
// first arrival time, not its body, so a later native echo cannot move history.
type inputTurn struct {
	TurnID    string                 `json:"turn_id,omitempty"`
	Timestamp *timestamppb.Timestamp `json:"timestamp,omitempty"`
}

type pendingUserMessage struct {
	ID          string                             `json:"id"`
	AgentWorkID string                             `json:"agent_work_id"`
	Timestamp   *timestamppb.Timestamp             `json:"timestamp"`
	Content     *dispatcherpublicv1.MessagePayload `json:"content"`
}

type MessageAttribution struct {
	TurnID string  `json:"turn_id,omitempty"`
	Agent  *string `json:"agent,omitempty"`
}

// Attribute also consumes non-product command/task facts. A native replay of a
// queued user input may arrive before its started fact; defer that projection
// until the actual turn is known, retaining the original content and timestamp.
func (s *MessageProjection) Attribute(native *Message, messages []*dispatcherpublicv1.UnifiedMessage, harnessSessionID, streamID string, offset int64) []*dispatcherpublicv1.UnifiedMessage {
	if s.InputTurns == nil {
		s.InputTurns = map[string]inputTurn{}
	}
	if s.PendingInputs == nil {
		s.PendingInputs = map[string][]pendingUserMessage{}
	}
	if s.Tools == nil {
		s.Tools = map[string]MessageAttribution{}
	}
	if s.Agents == nil {
		s.Agents = map[string]MessageAttribution{}
	}
	if s.Requests == nil {
		s.Requests = map[string]MessageAttribution{}
	}
	identity := native.UUID
	if identity == "" {
		identity = streamID + ":" + strconv.FormatInt(offset, 10)
	}
	stableID := func(kind, id string) string {
		return uuid.NewSHA1(uuid.NameSpaceOID, []byte(harnessSessionID+":"+kind+":"+id)).String()
	}
	root := ""
	if isInputDisposition(native) {
		input, known := s.InputTurns[native.CommandUUID]
		if !known && native.CommandState == "started" {
			if s.CurrentTurnID == "" {
				s.CurrentTurnID = stableID("turn", native.CommandUUID)
			}
			input.TurnID = s.CurrentTurnID
		}
		pending := s.PendingInputs[native.CommandUUID]
		if len(pending) == 0 && input.Timestamp == nil {
			for _, message := range messages {
				if message.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER {
					pending = append(pending, pendingUserMessage{ID: stableID("message", native.CommandUUID+":0"), AgentWorkID: message.AgentWorkId, Timestamp: message.Timestamp, Content: message.GetMessage()})
				}
			}
		}
		delete(s.PendingInputs, native.CommandUUID)
		var ready []*dispatcherpublicv1.UnifiedMessage
		for _, pending := range pending {
			if input.Timestamp == nil {
				input.Timestamp = pending.Timestamp
			}
			ready = append(ready, &dispatcherpublicv1.UnifiedMessage{Id: pending.ID, AgentWorkId: pending.AgentWorkID, Timestamp: input.Timestamp, TurnId: input.TurnID, Agent: &root, Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER, Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: pending.Content}})
		}
		s.InputTurns[native.CommandUUID] = input
		return ready
	}
	if native.Type == "system" && native.Subtype == "task_started" && native.TaskID != "" && native.ToolUseID != "" && (native.TaskType == "local_agent" || native.TaskType == "in_process_teammate") {
		owner := MessageAttribution{TurnID: stableID("agent-turn", native.ToolUseID), Agent: &native.TaskID}
		// A resumed member can keep its original parent reference. Only its new
		// native task start advances these aliases; historical tool owners stay put.
		// ponytail: scans delegation aliases per member start; index by native task
		// if very large member populations make this infrequent scan material.
		for call, prior := range s.Agents {
			if prior.Agent != nil && *prior.Agent == native.TaskID {
				s.Agents[call] = owner
			}
		}
		s.Agents[native.ToolUseID] = owner
	}
	isRoot := native.ParentToolUseID == "" && native.AgentID == ""
	if isRoot && s.CurrentTurnID == "" && ((native.Type == "stream_event" && native.Event.Type == "message_start") || (native.Type == "assistant" && hasAssistantContent(messages))) {
		s.CurrentTurnID = stableID("turn", identity)
	}
	attribution := MessageAttribution{}
	switch {
	case native.ParentToolUseID != "":
		attribution = s.Agents[native.ParentToolUseID]
	case native.AgentID != "":
		attribution.Agent = &native.AgentID
	case native.Type == "user" || native.Type == "assistant" || native.Type == "result" || native.Type == "stream_event":
		attribution = MessageAttribution{TurnID: s.CurrentTurnID, Agent: &root}
	}
	if native.AgentID != "" {
		attribution.Agent = &native.AgentID
	}
	input, inputKnown := s.InputTurns[native.UUID]
	// Injected context has no input command lifecycle to wait for.
	inputReplay := isRoot && native.Type == "user" && native.IsReplay && !native.IsSynthetic && !native.IsMeta
	if inputReplay {
		attribution.TurnID = input.TurnID
	}
	for index, message := range messages {
		message.Id = messageID(streamID, offset, index)
		if inputReplay {
			// A process launched later writes an unsettled input again; only its
			// native identity, not its position, matches the first projection.
			message.Id = stableID("message", identity+":"+strconv.Itoa(index))
		}
		// Only a replayed Talgent input is a person's; other text in the
		// conversation the Harness wrote itself.
		if isRoot && native.Type == "user" && !inputReplay && message.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER {
			message.Kind = dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_HARNESS_NOTE
		}
		if inputReplay && inputKnown && message.GetMessage() != nil {
			if input.Timestamp == nil {
				input.Timestamp = message.Timestamp
				s.InputTurns[native.UUID] = input
			}
			message.Timestamp = input.Timestamp
		}
		owner := attribution
		switch {
		case message.GetAction() != nil:
			s.Tools[message.GetAction().ToolCallId] = owner
		case message.GetObservation() != nil:
			// A tool's result is its last message; its attribution is no longer needed.
			owner = s.Tools[message.GetObservation().ToolCallId]
			delete(s.Tools, message.GetObservation().ToolCallId)
		case message.GetPermission() != nil:
			permission := message.GetPermission()
			if permission.Phase == dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_REQUESTED {
				owner = s.Tools[permission.ToolCallId]
				s.Requests[permission.PermissionId] = owner
			} else {
				owner = s.Requests[permission.PermissionId]
				delete(s.Requests, permission.PermissionId)
			}
		case message.GetInteraction() != nil:
			interaction := message.GetInteraction()
			if interaction.Phase == dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_REQUESTED {
				owner = s.Tools[interaction.ToolCallId]
				s.Requests[interaction.InteractionId] = owner
			} else {
				owner = s.Requests[interaction.InteractionId]
				delete(s.Requests, interaction.InteractionId)
			}
		}
		message.TurnId, message.Agent = owner.TurnID, owner.Agent
	}
	if inputReplay && !inputKnown && len(messages) > 0 {
		if _, pending := s.PendingInputs[native.UUID]; !pending {
			for _, message := range messages {
				if content := message.GetMessage(); content != nil {
					s.PendingInputs[native.UUID] = append(s.PendingInputs[native.UUID], pendingUserMessage{ID: message.Id, AgentWorkID: message.AgentWorkId, Timestamp: message.Timestamp, Content: content})
				}
			}
		}
		// Tool results retain their call attribution; they do not await an input turn.
		return slices.DeleteFunc(messages, func(message *dispatcherpublicv1.UnifiedMessage) bool {
			return message.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER
		})
	}
	if native.Type == "result" && isRoot {
		s.CurrentTurnID = ""
	}
	return messages
}

// messageID names a message by its position in the process's output, so a
// replay of the same line yields the same ID and, within one millisecond, ID
// order is output order. Messages of different processes do not share a
// millisecond; the process key only keeps their IDs apart.
func messageID(streamID string, offset int64, index int) string {
	process := sha1.Sum([]byte(streamID))
	return fmt.Sprintf("%016x-%04x-%x", offset, index, process[:4])
}

func hasAssistantContent(messages []*dispatcherpublicv1.UnifiedMessage) bool {
	for _, message := range messages {
		if message.GetMessage() != nil || message.GetAction() != nil {
			return true
		}
	}
	return false
}
