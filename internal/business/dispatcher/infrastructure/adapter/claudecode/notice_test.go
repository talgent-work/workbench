package claudecode

import (
	"context"
	"strings"
	"testing"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter"
)

// Intent (real): people reading a Work are told about model provider refusals
// the Harness retries or a SubAgent absorbs. Refusals that end the root Turn,
// or need someone to act, are explained by the Turn's failed Result instead;
// none of these frames is history.
func TestUpstreamNoticesOnlyRecoverableProviderRefusals(t *testing.T) {
	long := strings.Repeat("限", noticeMessageLimit+10)
	for _, tc := range []struct {
		name, raw string
		want      *adapter.Notice
	}{
		{"retry rate limit", `{"type":"system","subtype":"api_retry","error":"rate_limit","attempt":3}`, &adapter.Notice{Reason: adapter.NoticeModelRateLimited}},
		{"retry overloaded", `{"type":"system","subtype":"api_retry","error":"server_error"}`, &adapter.Notice{Reason: adapter.NoticeModelUnavailable}},
		{"retry uncategorized", `{"type":"system","subtype":"api_retry"}`, nil},
		{"retry needing a person", `{"type":"system","subtype":"api_retry","error":"authentication_failed"}`, nil},
		{"subagent rate limit", `{"type":"assistant","is_api_error_message":true,"error":"rate_limit","parent_tool_use_id":"task-1","message":{"content":[{"type":"text","text":"API Error: 429 请控制请求频率"}]}}`, &adapter.Notice{Reason: adapter.NoticeModelRateLimited, Message: "API Error: 429 请控制请求频率"}},
		{"background agent unavailable", `{"type":"assistant","is_api_error_message":true,"api_error":"server_error","agent_id":"a71f","message":{"content":[]}}`, &adapter.Notice{Reason: adapter.NoticeModelUnavailable}},
		{"long provider text", `{"type":"assistant","is_api_error_message":true,"api_error":"rate_limit","parent_tool_use_id":"task-1","message":{"content":[{"type":"text","text":"` + long + `"}]}}`, &adapter.Notice{Reason: adapter.NoticeModelRateLimited, Message: long[:len(long)-10*len("限")]}},
		{"root rate limit ends the Turn", `{"type":"assistant","is_api_error_message":true,"error":"rate_limit","message":{"content":[{"type":"text","text":"API Error: 429"}]}}`, nil},
		{"subagent authentication needs a person", `{"type":"assistant","is_api_error_message":true,"error":"authentication_failed","parent_tool_use_id":"task-1","message":{"content":[]}}`, nil},
		{"usage limit is not recovered automatically", `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`, nil},
		{"ordinary prose", `{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`, nil},
		{"other system frame", `{"type":"system","subtype":"status","message":"ready"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages, got, err := UpstreamToMessages(context.Background(), "work-1", []byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("notice = %+v, want %+v", got, tc.want)
			}
			if tc.name != "ordinary prose" && len(messages) != 0 {
				t.Fatalf("refusal entered history: %v", messages)
			}
		})
	}
}
