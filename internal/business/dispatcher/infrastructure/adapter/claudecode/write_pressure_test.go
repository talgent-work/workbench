package claudecode_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
)

func TestInternalThinkingAndCommandEventsProduceNoProductWrites(t *testing.T) {
	for _, raw := range []string{
		`{"type":"system","subtype":"thinking_tokens","token_count":123}`,
		`{"type":"command_lifecycle","command_uuid":"input-1","state":"started"}`,
	} {
		messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(raw))
		require.NoError(t, err)
		require.Empty(t, messages, "internal progress must not become persisted UnifiedMessages: %s", raw)
	}
}
