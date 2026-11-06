package nativeprocess

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeReplayExpiredOffsetContinuesRetainedAndLiveMessages(t *testing.T) {
	// Even a suffix that parses as JSON is not a proven record boundary.
	fragment := `{"type":"result"}` + "\n"
	retained := `{"type":"control_cancel_request","request_id":"retained"}` + "\n"
	live := `{"type":"control_cancel_request","request_id":"live"}` + "\n"
	connection, ctx := protocolConnection(t, 431,
		replay(900, fragment+retained),
		`{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`,
		append([]byte{1}, live...),
	)
	gap, err := connection.Receive(ctx)
	require.NoError(t, err, "an evicted offset must not detach the process")
	require.Equal(t, "replay_gap", gap.Kind)
	require.Equal(t, int64(900), gap.Offset)
	require.Equal(t, int64(469), gap.SkippedBytes)

	discarded, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "discarded", discarded.Kind)
	require.Equal(t, int64(900+len(fragment)), discarded.Offset)
	message, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "retained", message.Message.RequestID)
	require.Equal(t, int64(900+len(fragment)+len(retained)), message.Offset)
	connected, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "connected", connected.Kind)
	message, err = connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "live", message.Message.RequestID)
	require.Equal(t, int64(900+len(fragment)+len(retained)+len(live)), message.Offset)
}

func TestNativeReplayGapDrainsFragmentAcrossFrames(t *testing.T) {
	for _, prefix := range []string{"", `partial {"type":`} {
		t.Run(prefix, func(t *testing.T) {
			fragmentEnd := `"result"}` + "\n"
			live := `{"type":"control_cancel_request","request_id":"live"}` + "\n"
			connection, ctx := protocolConnection(t, 431,
				replay(900, prefix),
				`{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`,
				append([]byte{1}, fragmentEnd...), append([]byte{1}, live...),
			)
			gap, err := connection.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, "replay_gap", gap.Kind)
			connected, err := connection.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, "connected", connected.Kind)
			discarded, err := connection.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, "discarded", discarded.Kind)
			require.Equal(t, int64(900+len(prefix)+len(fragmentEnd)), discarded.Offset)
			message, err := connection.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, "live", message.Message.RequestID)
			require.Equal(t, int64(900+len(prefix)+len(fragmentEnd)+len(live)), message.Offset)
		})
	}
}

func TestNativeReplayGapDropsBufferedBytesAndPreservesExit(t *testing.T) {
	connection, ctx := protocolConnection(t, 431,
		replay(431, `{"type":`), replay(900, `"result"`),
		`{"type":"exit","exit_code":7}`,
	)
	gap, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "replay_gap", gap.Kind)
	exit, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "exit", exit.Kind)
	require.Equal(t, 7, exit.ExitCode)
	require.True(t, exit.Discarded)
	require.Equal(t, int64(908), exit.Offset)
}

func TestNativeReplayTransportFailureDoesNotBecomeGap(t *testing.T) {
	connection, ctx := protocolConnection(t, 431, replay(431, `{"type":`))
	event, err := connection.Receive(ctx)
	require.Error(t, err)
	require.Empty(t, event.Kind)
}
