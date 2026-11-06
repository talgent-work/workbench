package nativeprocess

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// Input and replies live in the Work's input buffer, not in the stream's
// snapshot; the mirror only tells the reader which entries it can settle.
func TestSnapshotCarriesNoBufferedInput(t *testing.T) {
	state := NewProtocolState()
	state.Pending["asked"], state.Pending["answered"] = &Message{}, &Message{}
	state.MirrorBuffer([]command.BufferedInput{
		{ID: "019a0000-0000-7000-8000-000000000001", Text: "secret draft"},
		{ID: "answered", Reply: &command.BufferedReply{Allow: true}},
	})
	require.Equal(t, []string{"answered", "asked"}, state.PendingInteractions(), "pending requests are reported whether replied or not")
	require.Contains(t, state.Replies, "answered")

	binding := command.ConnectExecution{WorkID: "work", ProcessID: "process", NativeSessionID: "session"}
	raw, err := EncodeSnapshot(binding, state)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret draft")
	restored, err := RestoreState(binding, raw)
	require.NoError(t, err)
	require.Empty(t, restored.Inputs, "a restored reader reloads the buffer")
	require.Empty(t, restored.Replies)
	require.Equal(t, []string{"answered", "asked"}, restored.PendingInteractions())

	state.ForgetBuffered(command.BufferedInput{ID: "answered", Reply: &command.BufferedReply{}})
	require.NotContains(t, state.Replies, "answered")
}

// A recorded loss is resumed only from a state its last reader gave up: a
// state a replica resumed reading and never gave up may hide an attachment
// regained since, so its loss is forgotten on restore.
func TestALossIsResumedOnlyFromAGivenUpState(t *testing.T) {
	binding := command.ConnectExecution{WorkID: "work", ProcessID: "process", NativeSessionID: "session"}
	state := NewProtocolState()
	require.False(t, state.ResumeReading(), "nothing to protect without a loss")
	state.DetachedSince, state.DetachmentReported = time.Now().UTC().Truncate(time.Millisecond), true
	require.True(t, state.ResumeReading())

	raw, err := EncodeSnapshot(binding, state)
	require.NoError(t, err)
	restored, err := RestoreState(binding, raw)
	require.NoError(t, err)
	require.Zero(t, restored.DetachedSince, "a resumed reader may have attached again")
	require.False(t, restored.DetachmentReported)

	state.GiveUpReading()
	raw, err = EncodeSnapshot(binding, state)
	require.NoError(t, err)
	restored, err = RestoreState(binding, raw)
	require.NoError(t, err)
	require.True(t, state.DetachedSince.Equal(restored.DetachedSince), "a given-up state keeps its loss")
	require.True(t, restored.DetachmentReported)
}
