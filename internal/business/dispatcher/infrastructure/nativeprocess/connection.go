// Package nativeprocess translates native Claude stdio over execd pipe mode.
// It does not own AgentWork lifecycle or infer persistence from turn results.
package nativeprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/pkg/execd"
)

const maxMessageBytes = 16 << 20

var (
	ErrProtocol = errors.New("dispatcher: invalid native Claude protocol")
	ErrReplay   = errors.New("dispatcher: native replay starts before the expected offset")
)

type Message struct {
	Commands       json.RawMessage `json:"commands"`
	Plugins        json.RawMessage `json:"plugins"`
	TerminalReason string          `json:"terminal_reason"`
	AgentID        string          `json:"agent_id"`
	TaskID         string          `json:"task_id"`
	TaskType       string          `json:"task_type"`
	// Native task facts of a SubAgent's start and end.
	SubagentType    string          `json:"subagent_type"`
	Description     string          `json:"description"`
	ParentTaskID    string          `json:"parent_task_id"`
	SpawnDepth      int             `json:"spawn_depth"`
	TaskStatus      string          `json:"status"`
	ToolUseID       string          `json:"tool_use_id"`
	ToolUseResult   json.RawMessage `json:"tool_use_result"`
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	UUID            string          `json:"uuid"`
	IsReplay        bool            `json:"isReplay"`
	IsSynthetic     bool            `json:"isSynthetic"`
	IsMeta          bool            `json:"isMeta"`
	IsAPIError      bool            `json:"is_api_error_message"`
	IsError         bool            `json:"is_error"`
	ParentToolUseID string          `json:"parent_tool_use_id"`
	CommandUUID     string          `json:"command_uuid"`
	CommandState    string          `json:"state"`
	// PermissionMode is the mode a system init or status message reports.
	PermissionMode string `json:"permissionMode"`
	// Content is a system message's own content, such as the text of an
	// informational one. Raw: its shape is the message kind's.
	Content json.RawMessage `json:"content"`
	Event     struct {
		Type string `json:"type"`
	} `json:"event"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype               string            `json:"subtype"`
		ToolName              string            `json:"tool_name"`
		Title                 string            `json:"title"`
		ToolUseID             string            `json:"tool_use_id"`
		Input                 json.RawMessage   `json:"input"`
		PermissionSuggestions []json.RawMessage `json:"permission_suggestions"`
	} `json:"request"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
		Message   string `json:"message"`
		Response  struct {
			Commands json.RawMessage `json:"commands"`
			Behavior string          `json:"behavior"`
			// Mode is the permission mode a set_permission_mode answer applied.
			Mode    string `json:"mode"`
			Applied struct {
				Effort json.RawMessage `json:"effort"`
			} `json:"applied"`
		} `json:"response"`
	} `json:"response"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type Event struct {
	// The next raw log already carries this assistant's original text as Result.
	// Still apply protocol attribution/lifecycle, but omit its product text.
	RepeatedAssistant bool

	Kind      string
	Message   *Message
	Raw       []byte
	ExitCode  int
	Offset    int64
	Discarded bool
	// Replay gaps are observations, not checkpoint boundaries. Offset identifies
	// the first retained byte; the next discarded/message event ends a full line.
	SkippedBytes int64
}

// Connection has one reader; execd serializes concurrent writes. The owner must
// keep reading even while awaiting a control response so ping frames are served.
type receivedEvent struct {
	event Event
	err   error
}

type Connection struct {
	ahead  <-chan receivedEvent
	queued *receivedEvent

	pipe        *execd.Pipe
	nativeID    string
	buffer      []byte
	consumed    int64
	received    int64
	discardLine bool
}

func NewConnection(pipe *execd.Pipe, nativeID string, offset int64) *Connection {
	return &Connection{pipe: pipe, nativeID: nativeID, consumed: offset, received: offset}
}

// Receive looks ahead by one raw log for root assistant text. The socket read
// keeps running on timeout: cancelling it would detach the native process pipe.
// Unapplied offsets stay behind the buffered message, so reconnect replays it.
func (c *Connection) Receive(ctx context.Context) (Event, error) {
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	var current receivedEvent
	if c.queued != nil {
		current, c.queued = *c.queued, nil
	} else if c.ahead != nil {
		select {
		case current = <-c.ahead:
			c.ahead = nil
		case <-ctx.Done():
			return Event{}, ctx.Err()
		}
	} else {
		current.event, current.err = c.receive(ctx)
	}
	if current.err != nil {
		return current.event, current.err
	}
	text := assistantText(current.event)
	if text == "" {
		return current.event, nil
	}
	next := make(chan receivedEvent, 1)
	c.ahead = next
	go func() {
		event, err := c.receive(ctx)
		next <- receivedEvent{event, err}
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case following := <-next:
		c.ahead, c.queued = nil, &following
		if following.err == nil && following.event.Kind == "message" {
			message := following.event.Message
			if message.Type == "result" && message.ParentToolUseID == "" && message.AgentID == "" {
				var result struct {
					Text string `json:"result"`
				}
				if json.Unmarshal(following.event.Raw, &result) == nil && result.Text == text {
					current.event.RepeatedAssistant = true
				}
			}
		}
	case <-timer.C:
		// Flush ordinary commentary even when the Harness has no next log yet.
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
	return current.event, nil
}

func assistantText(event Event) string {
	message := event.Message
	if event.Kind != "message" || message == nil || message.Type != "assistant" || message.ParentToolUseID != "" || message.AgentID != "" || message.IsAPIError {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(message.Message.Content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return ""
	}
	return blocks[0].Text
}

func (c *Connection) receive(ctx context.Context) (Event, error) {
	for {
		if end := bytes.IndexByte(c.buffer, '\n'); end >= 0 {
			line := bytes.TrimSpace(c.buffer[:end])
			c.buffer = c.buffer[end+1:]
			c.consumed += int64(end + 1)
			if c.discardLine || len(line) > maxMessageBytes {
				c.discardLine = false
				return Event{Kind: "discarded", Offset: c.consumed, Discarded: true}, nil
			}
			if len(line) == 0 {
				continue
			}
			var msg Message
			if err := json.Unmarshal(line, &msg); err != nil || msg.Type == "" || (msg.SessionID != "" && msg.SessionID != c.nativeID) {
				return Event{Kind: "discarded", Offset: c.consumed, Discarded: true}, nil
			}
			return Event{Kind: "message", Message: &msg, Raw: bytes.Clone(line), Offset: c.consumed}, nil
		}
		if c.discardLine || len(c.buffer) > maxMessageBytes {
			c.discardLine = true
			c.consumed += int64(len(c.buffer))
			c.buffer = nil
		}
		frame, err := c.pipe.Receive(ctx)
		if err != nil {
			return Event{}, err
		}
		switch frame.Type {
		case "stdout":
			c.buffer = append(c.buffer, frame.Data...)
			c.received += int64(len(frame.Data))
		case "stderr":
			// Production launches route stderr to a separate diagnostic file.
			// Accepting it here would make subsequent replay offsets ambiguous.
			return Event{}, ErrProtocol
		case "replay":
			if frame.Offset < c.received {
				return Event{}, ErrReplay
			}
			if frame.Offset > c.received {
				gap := frame.Offset - c.received
				// execd clamps an expired cursor to its oldest retained byte. That
				// byte need not start a JSON line, even if the suffix parses as JSON.
				// Drop the old fragment and drain through the first newline before
				// exposing another checkpoint boundary. Keep using this attachment.
				c.buffer = append(c.buffer[:0], frame.Data...)
				c.consumed = frame.Offset
				c.received = frame.Offset + int64(len(frame.Data))
				c.discardLine = true
				return Event{Kind: "replay_gap", Offset: frame.Offset, SkippedBytes: gap}, nil
			}
			c.buffer = append(c.buffer, frame.Data...)
			c.received += int64(len(frame.Data))
		case "connected":
			return Event{Kind: "connected", Offset: c.consumed}, nil
		case "exit":
			// A truncated final line cannot hide the actual process exit.
			return Event{Kind: "exit", ExitCode: *frame.ExitCode, Offset: c.received, Discarded: c.discardLine || len(bytes.TrimSpace(c.buffer)) != 0}, nil
		}
	}
}

func (c *Connection) Initialize(ctx context.Context, requestID string) error {
	return c.control(ctx, requestID, "initialize")
}

func (c *Connection) Interrupt(ctx context.Context, requestID string) error {
	return c.control(ctx, requestID, "interrupt")
}

func (c *Connection) SetThinkingLevel(ctx context.Context, requestID, level string) error {
	if requestID == "" {
		return ErrProtocol
	}
	var effort any = level
	if level == "" {
		effort = nil
	}
	return c.send(ctx, map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{
		"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": effort},
	}})
}

// SetPermissionMode asks the Harness to switch its permission mode at once.
func (c *Connection) SetPermissionMode(ctx context.Context, requestID, mode string) error {
	if requestID == "" || mode == "" {
		return ErrProtocol
	}
	return c.send(ctx, map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{
		"subtype": "set_permission_mode", "mode": mode,
	}})
}

func (c *Connection) GetThinkingLevel(ctx context.Context, requestID string) error {
	return c.control(ctx, requestID, "get_settings")
}

func (c *Connection) control(ctx context.Context, requestID, subtype string) error {
	if requestID == "" {
		return ErrProtocol
	}
	return c.send(ctx, map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{"subtype": subtype}})
}

// UserInput writes one input exactly as given.
func (c *Connection) UserInput(ctx context.Context, id, text string) error {
	if id == "" {
		return ErrProtocol
	}
	return c.send(ctx, map[string]any{
		"type": "user", "session_id": c.nativeID, "uuid": id, "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user", "content": text},
	})
}

// NewToolResponse uses the original native request when no replacement input is supplied.
// Always-allow permissions use native suggestions rather than invented rule shapes.
// A denial's reason and a question's answers reach the Agent exactly as the
// human gave them.
type ToolResponse struct{ data []byte }

func NewToolResponse(request *Message, allow, always bool, reason string, input json.RawMessage) (ToolResponse, error) {
	if request == nil || request.Type != "control_request" || request.RequestID == "" || request.Request.Subtype != "can_use_tool" {
		return ToolResponse{}, ErrProtocol
	}
	response := map[string]any{"behavior": "deny", "message": reason}
	if allow {
		if len(input) == 0 {
			input = request.Request.Input
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(input, &object) != nil || object == nil {
			return ToolResponse{}, ErrProtocol
		}
		response = map[string]any{"behavior": "allow", "updatedInput": input}
		if always {
			if len(request.Request.PermissionSuggestions) == 0 {
				return ToolResponse{}, ErrProtocol
			}
			response["updatedPermissions"] = request.Request.PermissionSuggestions
		}
	}
	data, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": request.RequestID, "response": response,
	}})
	if err != nil {
		return ToolResponse{}, ErrProtocol
	}
	return ToolResponse{data: append(data, '\n')}, nil
}

func (c *Connection) ReplyTool(ctx context.Context, reply ToolResponse) error {
	if len(reply.data) == 0 {
		return ErrProtocol
	}
	return c.pipe.WriteInput(ctx, reply.data)
}

func (c *Connection) send(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return ErrProtocol
	}
	return c.pipe.WriteInput(ctx, append(data, '\n'))
}

func (c *Connection) Exit(ctx context.Context) error {
	return c.pipe.Signal(ctx, "SIGINT")
}
func (c *Connection) Close() error { return c.pipe.Close() }
