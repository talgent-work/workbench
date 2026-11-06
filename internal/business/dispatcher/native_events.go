package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// applyEvent folds one stream event into memory; the caller holds entry.mu.
// Only boundaries do I/O: readiness, a turn result and exit snapshot the
// state, a turn RESULT is stored before its turn is reported finished, and a
// settled input leaves the buffer before it is reported. Everything else is
// handed to asynchronous delivery.
func (m *NativeExecutions) applyEvent(ctx, startup context.Context, entry *nativeExecution, initializeID string, event nativeprocess.Event) error {
	if event.Discarded {
		m.logger.WarnContext(ctx, "discarded invalid native message", "agent_work_id", entry.target.WorkID, "offset", event.Offset)
	}
	switch event.Kind {
	case "replay_gap":
		m.logger.WarnContext(ctx, "native replay offset expired; continuing retained output",
			"agent_work_id", entry.target.WorkID, "offset", event.Offset, "skipped_bytes", event.SkippedBytes)
		// The replay begins at an arbitrary byte. Keep the cursor until framing
		// reaches a newline, so another resume cannot start inside the fragment.
		return nil
	case "discarded":
		entry.Offset, entry.dirty = event.Offset, true
		return nil
	case "connected":
		// Replay has caught up. A new connection writes every buffered entry
		// again; the Harness deduplicates them by identity.
		entry.attached, entry.written = true, map[string]bool{}
		if err := m.attachmentReturned(ctx, entry); err != nil {
			return err
		}
		if entry.Failure != "" {
			return m.exitFailedInitialization(ctx, entry)
		}
		if !entry.Ready {
			return entry.connection.Initialize(startup, initializeID)
		}
		if err := m.advanceTurnCancel(ctx, entry); err != nil {
			return err
		}
		return m.flush(ctx, entry)
	case "message":
		return m.applyMessage(ctx, entry, initializeID, event)
	case "exit":
		entry.Exited, entry.ExitCode, entry.Offset, entry.dirty = true, event.ExitCode, event.Offset, true
		m.settleAtExit(ctx, entry)
		return m.reportExit(ctx, entry)
	}
	return nil
}

func (m *NativeExecutions) applyMessage(ctx context.Context, entry *nativeExecution, initializeID string, event nativeprocess.Event) error {
	wasReady := entry.Ready
	// observe removes resolved requests; retain only this native frame's correlation.
	var controlRequest *nativeprocess.Message
	switch event.Message.Type {
	case "control_cancel_request":
		controlRequest = entry.Pending[event.Message.RequestID]
	case "control_response":
		controlRequest = entry.Pending[event.Message.Response.RequestID]
	}
	// Settings replies are private: their bodies may hold native configuration.
	privateSettings := event.Message.Type == "control_response" && privateSetting(event.Message.Response.RequestID)
	var messages []*dispatcherpublicv1.UnifiedMessage
	var notice *adapter.Notice
	var projectionErr error
	if !privateSettings {
		messages, notice, projectionErr = unifiedMessages(ctx, entry.target.WorkID, event.Raw, controlRequest)
		messages = entry.AppendCommandInput(entry.target.WorkID, event.Message, messages)
		m.publishNotice(ctx, entry.target.WorkID, notice)
	}
	now := timestamppb.New(time.Now().UTC().Truncate(time.Millisecond))
	for _, message := range messages {
		message.Timestamp = now
	}
	observations, err := m.observe(ctx, entry, initializeID, event.Message)
	if err != nil {
		if !errors.Is(err, nativeprocess.ErrProtocol) || privateSettings {
			return err
		}
		entry.Offset, entry.dirty = event.Offset, true
		if event.Message.Type == "control_response" && event.Message.Response.RequestID == initializeID && event.Message.Response.Subtype == "error" {
			text := event.Message.Response.Error
			if text == "" {
				text = event.Message.Response.Message
			}
			// The exit fact carries the Harness's own explanation to Agent.
			entry.Failure = "native_initialization_failed"
			if text != "" {
				// Bounded: it is retained in the snapshot and on the Work.
				entry.Failure += ": " + strings.ToValidUTF8(text[:min(len(text), 1024)], "")
			}
			return m.exitFailedInitialization(ctx, entry)
		}
		m.logger.WarnContext(ctx, "discarded invalid native message", "agent_work_id", entry.target.WorkID, "offset", event.Offset)
		return nil
	}
	entry.ObserveCapabilities(event.Message, initializeID)
	if progress, updated := entry.ObserveWorkProgress(event.Message); updated {
		observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionWorkProgressUpdated, WorkProgress: progress})
	}
	if subAgents, updated := entry.ObserveSubAgents(event.Message); updated {
		observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionSubAgentsUpdated, SubAgents: subAgents})
	}
	if mode := reportedPermissionMode(event.Message); mode != "" {
		if changed, known := entry.ObservePermissionMode(mode); changed {
			observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionPermissionModeObserved, PermissionMode: mode})
		} else if !known {
			m.logger.WarnContext(ctx, "native permission mode unknown; the Work keeps its last known mode", "agent_work_id", entry.target.WorkID, "mode", mode)
		}
	}
	if plugins, observed := nativeprocess.PluginLoads(event.Message); observed {
		observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionPluginLoadsObserved, LoadedPlugins: plugins})
	}
	entry.ObservePhase(event.Offset, observations)
	resultStored := true
	if !privateSettings {
		messages = entry.Projection.Attribute(event.Message, messages, entry.target.NativeSessionID, entry.target.ProcessID, event.Offset)
		if event.RepeatedAssistant {
			messages = slices.DeleteFunc(messages, func(message *dispatcherpublicv1.UnifiedMessage) bool {
				return message.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT
			})
		}
		if projectionErr != nil {
			m.logger.WarnContext(ctx, "native message projection failed", "agent_work_id", entry.target.WorkID, "error", projectionErr)
		}
		resultStored = m.project(ctx, entry, messages)
	}
	turnEnded := false
	for _, observation := range observations {
		if observation.Kind == command.ExecutionTurnFinished {
			// A turn whose RESULT could not be stored is not reported completed.
			observation.TurnCompleted = observation.TurnCompleted && resultStored
			turnEnded = true
		}
		observation.ObservationSequence = event.Offset
		m.report(entry, observation)
	}
	if !turnEnded {
		m.reportTurnActivity(entry, event.Message)
	}
	entry.Offset, entry.dirty = event.Offset, true
	if entry.Failure != "" {
		return m.exitFailedInitialization(ctx, entry)
	}
	if (!wasReady && entry.Ready) || turnEnded {
		if err := m.snapshot(ctx, entry); err != nil {
			return err
		}
	}
	// Readiness, or a finished Turn cancellation, lets due input be written.
	if (!wasReady && entry.Ready) || entry.flushDue {
		return m.flush(ctx, entry)
	}
	return nil
}

// turnActivityInterval bounds how often Turn activity is reported to Agent.
const turnActivityInterval = time.Minute

// reportTurnActivity tells Agent, at most once a minute, that the Work's Turn
// progresses: live model or tool output of a ready Harness. Agent only ends
// the Work's idleness on it, so the report carries nothing but the Work, and
// replayed output reports nothing. The caller holds entry.mu.
func (m *NativeExecutions) reportTurnActivity(entry *nativeExecution, message *nativeprocess.Message) {
	switch message.Type {
	case "assistant", "user", "stream_event":
	default:
		return
	}
	now := time.Now()
	if !entry.attached || !entry.Ready || entry.Exited || now.Sub(entry.activityReportedAt) < turnActivityInterval {
		return
	}
	entry.activityReportedAt = now
	m.report(entry, command.ExecutionObservation{Kind: command.ExecutionTurnActivity})
}

// A native initialization failure asks the Harness to exit; its exit fact
// carries the failure. Reconnect repeats the same exit request without
// writing input to a failed Harness.
func (m *NativeExecutions) exitFailedInitialization(ctx context.Context, entry *nativeExecution) error {
	if !entry.attached || entry.Exited {
		return nil
	}
	entry.exitSignalled = true
	if err := entry.connection.Exit(ctx); err != nil {
		return entry.disconnect(err)
	}
	return nil
}

// reportExit snapshots the exit and queues the exit fact, which is redelivered
// until Agent acknowledges it; Agent deletes the sandbox only afterwards.
// Whether the exit was normal is Agent's decision, recorded on the Work; a
// process diagnostic is not a conversation message.
func (m *NativeExecutions) reportExit(ctx context.Context, entry *nativeExecution) error {
	if entry.ExitAcknowledged {
		return nil
	}
	if err := m.snapshot(ctx, entry); err != nil {
		return err
	}
	m.report(entry, command.ExecutionObservation{Kind: command.ExecutionExited, ProcessID: entry.target.ProcessID, ExitCode: entry.ExitCode, ExitReason: entry.Failure})
	return nil
}

func (m *NativeExecutions) observe(ctx context.Context, entry *nativeExecution, initializeID string, message *nativeprocess.Message) ([]command.ExecutionObservation, error) {
	if entry.Failure != "" {
		return nil, nil
	}
	var observations []command.ExecutionObservation
	// A resolved or cancelled request settles its buffered reply.
	resolve := func(id string, outcome command.InputOutcome) {
		if _, ok := entry.Pending[id]; ok {
			delete(entry.Pending, id)
			observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionUserInteractionResolved, InteractionID: id})
		}
		m.settleBuffered(ctx, entry, id, true, outcome)
	}

	switch message.Type {
	case "command_lifecycle":
		// Native queue membership is not a turn start. Each merged input still
		// completes individually, so it settles only its own buffered entry.
		if _, buffered := entry.Inputs[message.CommandUUID]; !buffered {
			break
		}
		switch message.CommandState {
		case "started":
			if !entry.TurnObserved {
				entry.TurnObserved, entry.Active = true, true
				observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionTurnStarted})
			}
		case "completed", "cancelled", "discarded", "refused":
			// A terminal native disposition settles this input, not a successful turn.
			// Never resend a cancelled/refused command merely because no user echo came.
			m.settleBuffered(ctx, entry, message.CommandUUID, false, command.InputCompleted)
		}
	case "assistant", "stream_event":
		// A top-level model response is evidence of actual work. Input writes
		// and native input receipts alone never start a Turn.
		if message.ParentToolUseID == "" && message.AgentID == "" && !entry.TurnObserved &&
			(message.Type == "assistant" || message.Event.Type == "message_start") {
			entry.TurnObserved, entry.Active = true, true
			observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionTurnStarted})
		}
	case "control_response":
		if message.Response.Subtype == "success" && (message.Response.Response.Behavior == "allow" || message.Response.Response.Behavior == "deny") {
			resolve(message.Response.RequestID, command.InputCompleted)
		}
		if entry.CancelTurnID != "" && message.Response.RequestID == "talgent-cancel-"+entry.CancelTurnID {
			if message.Response.Subtype != "success" {
				return nil, nativeprocess.ErrProtocol
			}
			entry.CancelAck = true
		}
		if response, ok := entry.settings[message.Response.RequestID]; ok {
			response <- settingFromResponse(message)
			delete(entry.settings, message.Response.RequestID)
		}
		if message.Response.RequestID == initializeID {
			if message.Response.Subtype != "success" {
				return nil, nativeprocess.ErrProtocol
			}
			if !entry.Ready {
				entry.Ready = true
				observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionPrepared}, command.ExecutionObservation{Kind: command.ExecutionStarted})
			}
		}
	case "result":
		if message.ParentToolUseID == "" && message.AgentID == "" {
			entry.Active, entry.TurnObserved = false, false
			observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionTurnFinished, TurnCompleted: message.Subtype == "success" && !message.IsError && message.TerminalReason != "aborted_streaming" && message.TerminalReason != "aborted_tools"})
		}
	case "control_request":
		if message.Request.Subtype == "can_use_tool" {
			if message.RequestID == "" {
				return nil, nativeprocess.ErrProtocol
			}
			if _, ok := entry.Pending[message.RequestID]; !ok {
				entry.Pending[message.RequestID] = message
				observations = append(observations, command.ExecutionObservation{Kind: command.ExecutionUserInteractionRequired, InteractionID: message.RequestID})
			}
		}
	case "control_cancel_request":
		resolve(message.RequestID, command.InputClosed)
	case "user":
		var blocks []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(message.Message.Content, &blocks) == nil {
			for _, block := range blocks {
				if block.Type != "tool_result" {
					continue
				}
				for id, pending := range entry.Pending {
					if pending.Request.ToolUseID != "" && pending.Request.ToolUseID == block.ToolUseID {
						resolve(id, command.InputCompleted)
					}
				}
			}
		}
	}
	return observations, nil
}

func unifiedMessages(ctx context.Context, workID string, raw []byte, controlRequest *nativeprocess.Message) ([]*dispatcherpublicv1.UnifiedMessage, *adapter.Notice, error) {
	if controlRequest != nil {
		message, err := claudecode.ControlOutcomeToMessage(workID, raw, controlRequest.Request.ToolName, controlRequest.Request.ToolUseID, controlRequest.Request.Input)
		if message != nil {
			return []*dispatcherpublicv1.UnifiedMessage{message}, nil, err
		}
		return nil, nil, err
	}
	return claudecode.UpstreamToMessages(ctx, workID, raw)
}

// project delivers a frame's messages; a turn RESULT is stored before project
// returns. It reports whether every RESULT was stored.
func (m *NativeExecutions) project(ctx context.Context, entry *nativeExecution, messages []*dispatcherpublicv1.UnifiedMessage) bool {
	stored := true
	for _, message := range messages {
		message.AgentWorkId = entry.target.WorkID
		if message.Kind != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT {
			m.delivery.Deliver(message)
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := m.delivery.DeliverResult(bounded, message); err != nil {
			m.logger.WarnContext(ctx, "turn result was not stored", "agent_work_id", entry.target.WorkID, "message_id", message.Id, "error", err)
			stored = false
		}
		cancel()
	}
	return stored
}

func (m *NativeExecutions) publishNotice(ctx context.Context, workID string, notice *adapter.Notice) {
	if notice == nil {
		return
	}
	if err := m.delivery.PublishNotice(ctx, workID, notice.Reason, notice.Message); err != nil {
		m.logger.WarnContext(ctx, "native notice publication failed", "agent_work_id", workID, "error", err)
	}
}

// settingFromResponse reads the Harness's answer to a settings request: the
// effort level it applied, or the permission mode it accepted.
func settingFromResponse(message *nativeprocess.Message) settingResult {
	if message.Response.Subtype != "success" {
		if strings.HasPrefix(message.Response.RequestID, permissionRequestPrefix) {
			return settingResult{err: fmt.Errorf("%w: the Harness refused the permission mode", command.ErrExecutionConnection)}
		}
		return settingResult{err: command.ErrExecutionNotReady}
	}
	if strings.HasPrefix(message.Response.RequestID, permissionRequestPrefix) {
		mode := message.Response.Response.Mode
		if mode == "" {
			return settingResult{err: command.ErrExecutionNotReady}
		}
		return settingResult{value: mode}
	}
	var level string
	if raw := message.Response.Response.Applied.Effort; len(raw) > 0 && json.Unmarshal(raw, &level) != nil {
		// A future/native numeric setting must not break the conversation
		// protocol merely because this product exposes named levels.
		return settingResult{err: command.ErrExecutionNotReady}
	}
	return settingResult{value: level}
}
