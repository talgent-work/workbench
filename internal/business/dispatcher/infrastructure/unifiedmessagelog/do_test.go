package unifiedmessagelog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUnifiedMessageRoundTripPreservesOriginalContentAndAttribution(t *testing.T) {
	for _, agent := range []*string{nil, proto.String(""), proto.String("native-child")} {
		original := &dispatcherpublicv1.UnifiedMessage{
			Id: "message", AgentWorkId: "work", TurnId: "turn", Agent: agent,
			Kind:      dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT,
			Timestamp: timestamppb.New(time.Unix(1700000000, 0)),
			Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{
				Content: []*dispatcherpublicv1.ContentBlock{{Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT, Text: "原文\n🙂"}},
			}},
		}
		stored, err := messageToDO(original)
		require.NoError(t, err)
		require.NotContains(t, stored.PayloadJSON, "agent_work_id")
		require.NotContains(t, stored.PayloadJSON, "turn_id")
		restored, err := doToMessage(stored)
		require.NoError(t, err)
		require.True(t, proto.Equal(original, restored))
	}
}

func TestMalformedMessageContentCannotBecomePartialHistory(t *testing.T) {
	_, err := doToMessage(&messageDO{ID: "message", AgentWorkID: "work", PayloadJSON: `{bad json`})
	require.Error(t, err)
	_, err = messageToDO(nil)
	require.Error(t, err)
}
