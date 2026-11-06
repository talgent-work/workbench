package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter"
)

// noticeMessageLimit bounds provider text carried by a live notice.
const noticeMessageLimit = 512

// fromSystem: only api_retry concerns people, as a notice that the Harness is
// retrying a refused model request. No system frame is history.
func fromSystem(env *envelope, raw []byte) (*adapter.Notice, error) {
	if env.Subtype != "api_retry" {
		return nil, nil
	}
	var frame struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, fmt.Errorf("claudecode: unmarshal api_retry: %w", err)
	}
	if reason := providerRefusalReason(frame.Error); reason != "" {
		return &adapter.Notice{Reason: reason}, nil
	}
	return nil, nil
}

// providerRefusalReason names refusals the provider lifts by itself. Others,
// such as authentication or billing, need someone to act and are not notices.
func providerRefusalReason(category string) string {
	switch category {
	case "rate_limit":
		return adapter.NoticeModelRateLimited
	case "server_error":
		return adapter.NoticeModelUnavailable
	}
	return ""
}

// subAgentRefusalNotice tells people a SubAgent's model request was refused.
// Other refusals surface where the parent Agent reports the SubAgent.
func subAgentRefusalNotice(category string, content []assistantBody) *adapter.Notice {
	reason := providerRefusalReason(category)
	if reason == "" {
		return nil
	}
	var text []string
	for _, block := range content {
		if block.Type == "text" {
			text = append(text, block.Text)
		}
	}
	return &adapter.Notice{Reason: reason, Message: truncateRunes(strings.Join(text, "\n"), noticeMessageLimit)}
}

func truncateRunes(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}
