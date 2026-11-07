package claudecode

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode/message"
)

func fromAssistant(sessionID string, env *envelope, raw []byte) ([]*dispatcherpublicv1.UnifiedMessage, *adapter.Notice, error) {
	var body struct {
		Message struct {
			Content []assistantBody `json:"content"`
		} `json:"message"`
		IsAPIError      bool   `json:"is_api_error_message"`
		APIError        string `json:"api_error"`
		Error           string `json:"error"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		AgentID         string `json:"agent_id"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, err
	}
	if body.IsAPIError {
		// A provider refusal is not the Agent's prose and never history. The
		// root Agent's ends its Turn, whose failed Result carries the same text;
		// a SubAgent's is absorbed by its parent, so people are only told.
		if body.ParentToolUseID == "" && body.AgentID == "" {
			return nil, nil, nil
		}
		return nil, subAgentRefusalNotice(firstNonEmpty(body.APIError, body.Error), body.Message.Content), nil
	}
	var out []*dispatcherpublicv1.UnifiedMessage
	newAssistantPayload := func() *dispatcherpublicv1.MessagePayload {
		return &dispatcherpublicv1.MessagePayload{}
	}
	payload := newAssistantPayload()
	flushMessage := func() {
		if len(payload.Content) == 0 {
			return
		}
		m := newMessageMsg(sessionID, env.Type, env.Subtype, raw, payload)
		out = append(out, m)
		payload = newAssistantPayload()
	}
	for _, b := range body.Message.Content {
		switch b.Type {
		case "text":
			payload.Content = append(payload.Content, &dispatcherpublicv1.ContentBlock{
				Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT,
				Text: b.Text,
			})
		case "tool_use":
			inputJSON := b.Input
			flushMessage()
			action := newActionMsg(sessionID, env.Type, b.Type, raw, &dispatcherpublicv1.ActionPayload{
				ToolCallId: b.ID,
				ToolName:   b.Name,
				Summary:    actionSummary(b.Input),
			})
			out = append(out, withDetail(action, string(inputJSON), dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_JSON))
		}
	}
	flushMessage()
	return out, nil, nil
}

// Only a short original input field reaches the message list. The complete
// input remains in the separately stored detail, including Write/Edit content.
func actionSummary(input json.RawMessage) string {
	var fields struct {
		Skill       string `json:"skill"`
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		Description string `json:"description"`
		Command     string `json:"command"`
		Query       string `json:"query"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
	}
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, value := range []string{fields.Skill, fields.FilePath, fields.Path, fields.Description, fields.Command, fields.Query, fields.Pattern, fields.URL} {
		if value == "" {
			continue
		}
		count := 0
		for i := range value {
			if count == 160 {
				return strings.Clone(value[:i]) + "…"
			}
			count++
		}
		return value
	}
	return ""
}

type assistantBody struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func fromUser(sessionID string, env *envelope, raw []byte) ([]*dispatcherpublicv1.UnifiedMessage, error) {
	// Decode only the content field; do not copy the enclosing message JSON.
	var body struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		// UUID is the message's Harness position: a tool result's
		// observation carries it so a Work may start from there.
		UUID        string `json:"uuid"`
		IsSynthetic bool   `json:"isSynthetic"`
		IsMeta      bool   `json:"isMeta"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	payload := &dispatcherpublicv1.MessagePayload{}
	var out []*dispatcherpublicv1.UnifiedMessage
	flushMessage := func() {
		if len(payload.Content) == 0 {
			return
		}
		out = append(out, newMessageMsg(sessionID, env.Type, env.Subtype, raw, payload))
		payload = &dispatcherpublicv1.MessagePayload{}
	}
	rc := skipWhitespace(body.Message.Content)
	if len(rc) > 0 && rc[0] == '"' {
		var s string
		if err := json.Unmarshal(rc, &s); err != nil {
			return nil, err
		}
		if !isSkillBody(s, body.IsSynthetic || body.IsMeta) {
			payload.Content = append(payload.Content, &dispatcherpublicv1.ContentBlock{
				Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT,
				Text: commandAsTyped(s),
			})
		}
	} else if len(rc) > 0 && rc[0] == '[' {
		var blocks []userBody
		if err := json.Unmarshal(rc, &blocks); err != nil {
			return nil, err
		}
		preview := toolPreview(raw, blocks)
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if isSkillBody(b.Text, body.IsSynthetic || body.IsMeta) {
					continue
				}
				payload.Content = append(payload.Content, &dispatcherpublicv1.ContentBlock{
					Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT,
					Text: commandAsTyped(b.Text),
				})
			case "tool_result":
				detail, format, err := toolResultDetail(b.Content)
				if err != nil {
					return nil, err
				}
				flushMessage()
				result := newObservationMsg(sessionID, env.Type, env.Subtype, raw, &dispatcherpublicv1.ObservationPayload{ToolCallId: b.ToolUseID, IsError: b.IsError, ContextPosition: body.UUID})
				result.ToolPreview = preview
				out = append(out, withDetail(result, detail, format))

			case "image":
				payload.Content = append(payload.Content, &dispatcherpublicv1.ContentBlock{
					Type:     dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_IMAGE,
					MimeType: b.Source.MediaType,
					Text:     b.Source.Data,
				})
			}
		}
	}

	flushMessage()
	return out, nil
}

// Claude 2.1.266 wraps expanded Skill instructions with this header. Context
// flags alone also mark notifications and must not hide those inputs. Require
// both the native context flag and Skill header; literal human text is retained.
func isSkillBody(text string, internal bool) bool {
	return internal && strings.HasPrefix(text, "Base directory for this skill: ")
}

// commandTag is one tag of Claude's record of a slash command it ran.
var commandTag = regexp.MustCompile(`<command-(message|name|args)>([\s\S]*?)</command-(?:message|name|args)>`)

// commandAsTyped shows a slash command as the person typed it: Claude echoes
// a command it runs as <command-message>, <command-name> and <command-args>
// tags instead of the typed text. Text made of anything more is kept as is.
func commandAsTyped(text string) string {
	if !strings.HasPrefix(strings.TrimSpace(text), "<command-") {
		return text
	}
	var name, args string
	for _, tag := range commandTag.FindAllStringSubmatch(text, -1) {
		switch tag[1] {
		case "name":
			name = strings.TrimSpace(tag[2])
		case "args":
			args = strings.TrimSpace(tag[2])
		}
	}
	if !strings.HasPrefix(name, "/") || strings.TrimSpace(commandTag.ReplaceAllString(text, "")) != "" {
		return text
	}
	if args == "" {
		return name
	}
	return name + " " + args
}

type userBody struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

func toolResultDetail(raw json.RawMessage) (string, dispatcherpublicv1.MessageDetailFormat, error) {
	content := skipWhitespace(raw)
	if len(content) == 0 {
		return "", dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, nil
	}
	if content[0] == '"' {
		var text string
		if err := json.Unmarshal(content, &text); err != nil {
			return "", 0, err
		}
		return text, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, nil
	}
	if !json.Valid(content) {
		return "", 0, fmt.Errorf("invalid tool result JSON")
	}
	return string(raw), dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_JSON, nil
}

func skipWhitespace(b []byte) []byte {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return b[i:]
		}
	}
	return b
}

func fromResult(sessionID string, raw []byte) ([]*dispatcherpublicv1.UnifiedMessage, error) {
	var r message.ResultContent
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	var messages []*dispatcherpublicv1.UnifiedMessage
	for _, text := range r.Errors {
		// Claude appends this internal terminal diagnostic even for a cancelled
		// AskUserQuestion. It is not a user-facing Harness/API failure.
		if strings.HasPrefix(text, "[ede_diagnostic] ") && !strings.ContainsAny(text, "\r\n") {
			continue
		}
		if text != "" {
			messages = append(messages, errorMessage(sessionID, &dispatcherpublicv1.ErrorPayload{Code: r.Subtype, Message: text}))
		}
	}
	messages = append(messages, newResultMsg(sessionID, "result", r.Subtype, raw, &dispatcherpublicv1.ResultPayload{
		Status: resultStatus(r), Text: r.Result, DurationMs: r.DurationMs,
	}))
	return messages, nil
}

func resultStatus(r message.ResultContent) dispatcherpublicv1.ResultStatus {
	if r.TerminalReason == "aborted_streaming" || r.TerminalReason == "aborted_tools" {
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_INTERRUPTED
	}
	if r.IsError {
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_FAILED
	}
	switch r.Subtype {
	case "success":
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_SUCCESS
	case "cancelled":
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_CANCELLED
	case "interrupted":
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_INTERRUPTED
	case "timeout", "timed_out":
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_TIMED_OUT
	case "requires_input":
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_REQUIRES_INPUT
	default:
		return dispatcherpublicv1.ResultStatus_RESULT_STATUS_SUCCESS
	}
}

func fromAuthStatus(sessionID string, raw []byte) (*dispatcherpublicv1.UnifiedMessage, error) {
	var status struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	if status.Error == "" {
		return nil, nil
	}
	return errorMessage(sessionID, &dispatcherpublicv1.ErrorPayload{
		Message: status.Error,
	}), nil
}
