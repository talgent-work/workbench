package claudecode

import (
	"context"
	"testing"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

func mustTranslate(t *testing.T, raw string) *dispatcherpublicv1.UnifiedMessage {
	t.Helper()
	msgs, _, err := UpstreamToMessages(context.Background(), "work-1", []byte(raw))
	if err != nil {
		t.Fatalf("translate err: %v", err)
	}
	if len(msgs) == 0 || msgs[0] == nil {
		t.Fatal("expected message, got nil")
	}
	return msgs[0]
}

func mustTranslateMessages(t *testing.T, raw string) []*dispatcherpublicv1.UnifiedMessage {
	t.Helper()
	msgs, _, err := UpstreamToMessages(context.Background(), "work-1", []byte(raw))
	if err != nil {
		t.Fatalf("translate err: %v", err)
	}
	return msgs
}

func TestUpstream_AssistantMapsToTypedMessage(t *testing.T) {
	raw := `{"type":"assistant","message":{"id":"msg_runtime_1","model":"claude","content":[{"type":"text","text":"hi"}]},"parent_tool_use_id":"task-1"}`
	msg := mustTranslate(t, raw)

	if msg.GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT {
		t.Fatalf("kind = %v", msg.GetKind())
	}
	if msg.GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT {
		t.Fatalf("role = %v", msg.GetKind())
	}
	if got := msg.GetMessage().GetContent()[0].GetText(); got != "hi" {
		t.Fatalf("text = %q", got)
	}
}

func TestUpstream_AssistantToolUseMapsToAction(t *testing.T) {
	raw := `{"type":"assistant","message":{"id":"msg_runtime_1","model":"claude","content":[{"type":"text","text":"I'll inspect it."},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"cmd":"ls"}}]}}`
	msgs := mustTranslateMessages(t, raw)

	if len(msgs) != 2 {
		t.Fatalf("message count = %d, want 2", len(msgs))
	}
	if msgs[0].GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT {
		t.Fatalf("first kind = %v", msgs[0].GetKind())
	}
	content := msgs[0].GetMessage().GetContent()
	if len(content) != 1 {
		t.Fatalf("text message content blocks = %d, want only text block", len(content))
	}
	if content[0].GetType() != dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT || content[0].GetText() != "I'll inspect it." {
		t.Fatalf("text content = %+v", content[0])
	}
	action := msgs[1].GetAction()
	if action == nil {
		t.Fatalf("second payload = %T, want action", msgs[1].GetPayload())
	}
	if action.GetToolCallId() != "toolu_1" || action.GetToolName() != "Bash" || msgs[len(msgs)-1].Detail != `{"cmd":"ls"}` {
		t.Fatalf("action payload = %+v", action)
	}
}

// Intent: tool-only SubAgent frames must retain their parent Task tool_use so hierarchy can nest them.

func TestUpstream_AssistantToolOnlyEmitsOnlyAction(t *testing.T) {
	raw := `{"type":"assistant","message":{"id":"msg_runtime_1","model":"claude","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"cmd":"ls"}}]}}`
	msgs := mustTranslateMessages(t, raw)

	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1 action only", len(msgs))
	}
	if msgs[0].GetMessage() != nil {
		t.Fatalf("unexpected visible message for tool-only frame: %+v", msgs[0].GetMessage())
	}
	action := msgs[0].GetAction()
	if action == nil {
		t.Fatalf("payload = %T, want action", msgs[0].GetPayload())
	}
	if action.GetToolCallId() != "toolu_1" || action.GetToolName() != "Bash" || msgs[len(msgs)-1].Detail != `{"cmd":"ls"}` {
		t.Fatalf("action payload = %+v", action)
	}
}

func TestUpstream_AssistantTextSurvivesReasoningRemoval(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"before"},{"type":"thinking","thinking":"private"},{"type":"text","text":"after"}]}}`
	messages := mustTranslateMessages(t, raw)
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want one assistant message", len(messages))
	}
	blocks := messages[0].GetMessage().GetContent()
	if len(blocks) != 2 || blocks[0].GetText() != "before" || blocks[1].GetText() != "after" {
		t.Fatalf("original text blocks were changed: %v", blocks)
	}
}

func TestUpstream_UserToolResultMapsToObservation(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`
	msg := mustTranslate(t, raw)

	if msg.GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION {
		t.Fatalf("kind = %v", msg.GetKind())
	}
	if msg.GetObservation().GetToolCallId() != "t1" {
		t.Fatalf("tool call id = %q", msg.GetObservation().GetToolCallId())
	}
}

func TestUpstream_UserToolResultContentArrayMapsToObservation(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"ok"}]}]}}`
	msg := mustTranslate(t, raw)

	if msg.GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION {
		t.Fatalf("kind = %v", msg.GetKind())
	}
	if msg.Detail != `[{"type":"text","text":"ok"}]` {
		t.Fatalf("original tool content changed: %s", msg.Detail)
	}

}

func TestUpstream_UserMixedTextAndToolResultMapsToTwoSemanticMessages(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"done"},{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`
	msgs, _, err := UpstreamToMessages(context.Background(), "work-1", []byte(raw))
	if err != nil {
		t.Fatalf("translate err: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("message count = %d, want 2", len(msgs))
	}
	if msgs[0].GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER {
		t.Fatalf("first kind = %v", msgs[0].GetKind())
	}
	if msgs[1].GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION {
		t.Fatalf("second kind = %v", msgs[1].GetKind())
	}
}

// real unit: final Claude results must stay first-class work results so the
// Portal cannot render completion text as another assistant reply.
func TestUpstream_ResultMapsToFirstClassResult(t *testing.T) {
	raw := `{"type":"result","subtype":"success","is_error":false,"result":"Done","stop_reason":"end_turn","duration_ms":123,"duration_api_ms":99,"total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}`
	msg := mustTranslate(t, raw)

	if msg.GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT {
		t.Fatalf("kind = %v", msg.GetKind())
	}
	if msg.GetMessage() != nil {
		t.Fatalf("result must not be projected as assistant message: %+v", msg.GetMessage())
	}
	result := msg.GetResult()
	if result.GetStatus() != dispatcherpublicv1.ResultStatus_RESULT_STATUS_SUCCESS {
		t.Fatalf("result status = %v", result.GetStatus())
	}
}

func TestUpstream_ControlRequestCanUseToolMapsToPermission(t *testing.T) {
	raw := `{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"cmd":"ls"},"tool_use_id":"tu1","title":"Run command?"}}`
	msg := mustTranslate(t, raw)

	if msg.GetKind() != dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_PERMISSION {
		t.Fatalf("kind = %v", msg.GetKind())
	}
	if msg.GetPermission().GetPhase() != dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_REQUESTED {
		t.Fatalf("phase = %v", msg.GetPermission().GetPhase())
	}
}

func TestUpstream_KnownClaudeRuntimeTypesMapToSemanticKinds(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind dispatcherpublicv1.UnifiedMessageKind
	}{
		{"assistant", `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}]}}`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT},
		{"user_text", `{"type":"user","message":{"role":"user","content":"hi"}}`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER},
		{"user_tool_result", `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION},
		{"result", `{"type":"result","subtype":"success","result":"ok"}`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := mustTranslate(t, tc.raw)
			if msg.GetKind() != tc.kind {
				t.Fatalf("kind = %v, want %v", msg.GetKind(), tc.kind)
			}
		})
	}
}

func TestUpstream_KnownClaudeSystemSubtypesMapToSemanticKinds(t *testing.T) {
	tests := []struct {
		subtype string
		extra   string
		kind    dispatcherpublicv1.UnifiedMessageKind
	}{
		{"init", `"model":"claude","permission_mode":"default"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"compact_boundary", ``, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"status", `"message":"ready"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"api_retry", `"error":"rate_limit"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"local_command_output", `"command":"pwd","output":"/w"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"hook_started", `"hook_id":"h1","hook_event_name":"PreToolUse"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"hook_progress", `"hook_id":"h1","progress":"running"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"hook_response", `"hook_id":"h1","output":{}`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"plugin_install", `"plugin_id":"p1","status":"installed"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"task_started", `"task_id":"task1","tool_use_id":"tool1","description":"start"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"task_progress", `"task_id":"task1","tool_use_id":"tool1","description":"go"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"task_updated", `"task_id":"task1","tool_use_id":"tool1","update":"new"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"task_notification", `"task_id":"task1","tool_use_id":"tool1","status":"completed","summary":"done"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"session_state_changed", `"state":"ready"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"notification", `"message":"notice"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"files_persisted", `"files":["a"]`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"elicitation_complete", `"elicitation_id":"e1"`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
		{"unknown", `"detail":true`, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED},
	}

	for _, tc := range tests {
		t.Run(tc.subtype, func(t *testing.T) {
			raw := `{"type":"system","subtype":"` + tc.subtype + `"`
			if tc.extra != "" {
				raw += `,` + tc.extra
			}
			raw += `}`
			if tc.kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_UNSPECIFIED {
				if messages := mustTranslateMessages(t, raw); len(messages) != 0 {
					t.Fatalf("unsupported system subtype produced messages: %v", messages)
				}
				return
			}
			msg := mustTranslate(t, raw)
			if msg.GetKind() != tc.kind {
				t.Fatalf("kind = %v, want %v", msg.GetKind(), tc.kind)
			}
		})
	}
}

func TestUpstream_NativePermissionResponses(t *testing.T) {
	for _, test := range []struct {
		name, response string
		decision       dispatcherpublicv1.PermissionDecision
		reason         string
	}{
		{"allow", `{"subtype":"success","request_id":"permission-1","response":{"behavior":"allow","updatedInput":{"answers":{"Proceed?":"Yes"}}}}`, dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_ALLOW, ""},
		{"deny", `{"subtype":"success","request_id":"permission-2","response":{"behavior":"deny","message":"Not this time"}}`, dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_DENY, "Not this time"},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg, err := ControlOutcomeToMessage("work", []byte(`{"type":"control_response","response":`+test.response+`}`), "Bash", "tool", nil)
			if err != nil || msg == nil {
				t.Fatalf("outcome: %v, %v", msg, err)
			}
			permission := msg.GetPermission()
			if permission == nil || permission.Phase != dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_DECIDED || permission.Decision != test.decision || permission.Reason != test.reason {
				t.Fatalf("permission = %v", permission)
			}
			if test.name == "allow" && permission.PermissionId != "permission-1" || test.name == "deny" && permission.PermissionId != "permission-2" {
				t.Fatalf("request association = %s", permission.PermissionId)
			}
		})
	}
	for _, response := range []string{
		`{"subtype":"success","request_id":"init","response":{"agents":[]}}`,
		`{"subtype":"success","request_id":"interrupt","response":{}}`,
		`{"subtype":"success","request_id":"other","request_subtype":"set_model","response":{"behavior":"allow"}}`,
		`{"subtype":"success","request_id":"unknown","request_subtype":"can_use_tool","response":{"behavior":"unknown"}}`,
		`{"subtype":"success","request_id":"missing-behavior","request_subtype":"can_use_tool","response":{}}`,
		`{"subtype":"success","response":{"behavior":"allow"}}`,
	} {
		if messages := mustTranslateMessages(t, `{"type":"control_response","response":`+response+`}`); len(messages) != 0 {
			t.Fatalf("unexpected UnifiedMessage for %s: %v", response, messages)
		}
	}
}

func TestUpstream_UnsupportedEventsDoNotBecomeUnifiedMessages(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown event":          `{"type":"future_event","data":{"large":"internal payload"}}`,
		"unknown system subtype": `{"type":"system","subtype":"future_subtype","data":"internal payload"}`,
		"thinking tokens":        `{"type":"system","subtype":"thinking_tokens","count":1}`,
		"heartbeat":              `{"type":"keep_alive"}`,
		"startup":                `{"type":"system","subtype":"init","model":"claude"}`,
		"hook progress":          `{"type":"system","subtype":"hook_progress","progress":"running"}`,
		"storage notification":   `{"type":"system","subtype":"files_persisted"}`,
		"reasoning":              `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"private reasoning","signature":"private signature"}]}}`,
		"reasoning delta":        `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"private reasoning"}}}`,
		"stream bookkeeping":     `{"type":"stream_event","event":{"type":"message_stop"}}`,
		"tool summary":           `{"type":"tool_use_summary","summary":"internal summary"}`,
		"prompt suggestion":      `{"type":"prompt_suggestion","suggestion":"suggested next step"}`,
	} {
		t.Run(name, func(t *testing.T) {
			messages := mustTranslateMessages(t, raw)
			if len(messages) != 0 {
				t.Fatalf("unsupported native event produced %d UnifiedMessages: %v", len(messages), messages)
			}
		})
	}
}

func TestUpstream_MultipleToolResultsKeepTheirOwnCallAndOutcome(t *testing.T) {
	messages := mustTranslateMessages(t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"read-1","content":"first file"},{"type":"tool_result","tool_use_id":"bash-2","is_error":true,"content":"command failed"}]}}`)
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want one result for each of the two calls", len(messages))
	}
	first, second := messages[0].GetObservation(), messages[1].GetObservation()
	if first.GetToolCallId() != "read-1" || first.GetIsError() || messages[0].Detail != "first file" {
		t.Fatalf("first result lost its call or content: %v", first)
	}
	if second.GetToolCallId() != "bash-2" || !second.GetIsError() || messages[1].Detail != "command failed" {
		t.Fatalf("second result lost its call, failure or content: %v", second)
	}
}

// Claude 2.1.266's assistant envelope exposes is_api_error_message and api_error
// alongside message. A provider refusal is neither the Agent's prose nor
// history: a retried one is a live notice and one that ends the Turn reaches
// the stream through the Turn's failed Result.
func TestUpstream_HarnessAPIErrorIsNotHistory(t *testing.T) {
	for _, raw := range []string{
		`{"type":"assistant","is_api_error_message":true,"api_error":"max_output_tokens","message":{"content":[{"type":"text","text":"The model reached its output token limit."}]}}`,
		`{"type":"assistant","is_api_error_message":true,"error":"authentication_failed","message":{"content":[{"type":"text","text":"Failed to authenticate. API Error: 401 fixture API credential rejected"}]}}`,
		`{"type":"assistant","is_api_error_message":true,"error":"rate_limit","message":{"content":[{"type":"text","text":"API Error: 429"}]}}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`,
	} {
		if got := mustTranslateMessages(t, raw); len(got) != 0 {
			t.Fatalf("provider refusal entered history: %s -> %v", raw, got)
		}
	}
}

func TestUpstream_AuthenticationStatusOnlyPublishesActualFailure(t *testing.T) {
	for _, raw := range []string{
		`{"type":"auth_status","isAuthenticating":true,"output":["Opening browser"]}`,
		`{"type":"auth_status","isAuthenticating":false,"output":["Authenticated"]}`,
	} {
		if got := mustTranslateMessages(t, raw); len(got) != 0 {
			t.Fatalf("authentication bookkeeping entered product history: %v", got)
		}
	}
	msg := mustTranslate(t, `{"type":"auth_status","isAuthenticating":false,"error":"Invalid API key"}`)
	if msg.GetError() == nil || msg.GetError().GetMessage() != "Invalid API key" {
		t.Fatalf("native authentication failure was lost: %v", msg)
	}
}

// Native failure Results may carry errors[] without result prose. Dropping that
// field leaves users with a failed turn but no underlying explanation.
func TestUpstream_ResultFailureRetainsOriginalErrors(t *testing.T) {
	messages := mustTranslateMessages(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["Provider connection timed out.\nRequest: abc","Token budget exhausted."],"usage":{"input_tokens":0}}`)
	if len(messages) != 3 {
		t.Fatalf("want two original errors and the native Result, got %v", messages)
	}
	for i, want := range []string{"Provider connection timed out.\nRequest: abc", "Token budget exhausted."} {
		if messages[i].GetError().GetMessage() != want || messages[i].GetError().GetCode() != "error_during_execution" || messages[i].Detail != "" {
			t.Fatalf("lost original error: %v", messages[i])
		}
	}
	result := messages[2].GetResult()
	if result.GetStatus() != dispatcherpublicv1.ResultStatus_RESULT_STATUS_FAILED || result.GetText() != "" {
		t.Fatalf("result must keep native status and content: %v", result)
	}
}

func TestUpstream_ResultOmitsInternalDiagnostics(t *testing.T) {
	for _, reason := range []string{"aborted_tools", "aborted_streaming", ""} {
		raw := `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"` + reason + `","errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use","Provider connection timed out.\nRequest: abc","Token budget exhausted."]}`
		messages := mustTranslateMessages(t, raw)
		if len(messages) != 3 || messages[0].GetError().GetMessage() != "Provider connection timed out.\nRequest: abc" || messages[1].GetError().GetMessage() != "Token budget exhausted." {
			t.Fatalf("internal diagnostic leaked or original provider errors changed: %v", messages)
		}
		want := dispatcherpublicv1.ResultStatus_RESULT_STATUS_INTERRUPTED
		if reason == "" {
			want = dispatcherpublicv1.ResultStatus_RESULT_STATUS_FAILED
		}
		if messages[2].GetResult().GetStatus() != want {
			t.Fatalf("native result status changed: %v", messages[2])
		}
	}
	msg := mustTranslate(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools","errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use"]}`)
	if msg.GetResult().GetStatus() != dispatcherpublicv1.ResultStatus_RESULT_STATUS_INTERRUPTED {
		t.Fatalf("cancelled question must retain only the interrupted result: %v", msg)
	}
}

func TestUpstream_UnsupportedControlsDoNotBecomeActionableQuestions(t *testing.T) {
	for _, raw := range []string{
		`{"type":"control_request","request_id":"e","request":{"subtype":"elicitation","elicitation_id":"e","message":"Authenticate","requested_schema":{"type":"object"}}}`,
		`{"type":"control_request","request_id":"d","request":{"subtype":"request_user_dialog","dialog_id":"d","schema":{"type":"object"}}}`,
		`{"type":"control_request","request_id":"q","request":{"subtype":"side_question","question_id":"q","question":"Continue?"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_subtype":"elicitation","response":{"elicitation_id":"e","action":"accept"}}}`,
		`{"type":"control_response","response":{"subtype":"success","request_subtype":"side_question","response":{"question_id":"q","action":"accept"}}}`,
		`{"type":"system","subtype":"elicitation_complete","elicitation_id":"e"}`,
	} {
		if messages := mustTranslateMessages(t, raw); len(messages) != 0 {
			t.Fatalf("unsupported control entered product history: %s", raw)
		}
	}
}

// Claude's terminal_reason distinguishes interruption even when subtype is
// success and is_error is false; cancellation is not a successful answer.
func TestUpstream_NativeAbortedResultsAreInterrupted(t *testing.T) {
	for _, reason := range []string{"aborted_streaming", "aborted_tools"} {
		message := mustTranslate(t, `{"type":"result","subtype":"success","is_error":false,"terminal_reason":"`+reason+`","result":"Original terminal text"}`)
		if message.GetResult().Status != dispatcherpublicv1.ResultStatus_RESULT_STATUS_INTERRUPTED || message.GetResult().Text != "Original terminal text" {
			t.Fatalf("interruption was misclassified: %v", message.GetResult())
		}
	}
}
