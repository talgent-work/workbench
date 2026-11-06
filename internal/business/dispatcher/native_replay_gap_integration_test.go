//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"golang.org/x/net/websocket"
)

// Recovery must restore the queryable connection and message delivery, including
// when another disconnect happens before the first safe JSON boundary is found.
func TestReplayGapRecoversConnectionAndSnapshot(t *testing.T) {
	replicas := newNativeReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fragment := `{"type":"result"}` + "\n"
	retained := `{"type":"assistant","uuid":"retained","message":{"content":[{"type":"text","text":"retained answer"}]}}` + "\n"
	live := `{"type":"result","subtype":"success","result":"live answer"}` + "\n"
	sendLive := make(chan struct{})
	defer close(sendLive)
	attempts := make(chan string, 2)
	var attachments atomic.Int32
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		attempt := attachments.Add(1)
		attempts <- ws.Request().URL.Query().Get("since")
		data := fragment + retained
		if attempt == 1 {
			data = fragment[:5] // Disconnect before the first newline.
		}
		frame := make([]byte, 9)
		frame[0] = 3
		binary.BigEndian.PutUint64(frame[1:], 900)
		if websocket.Message.Send(ws, append(frame, data...)) != nil ||
			websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		if attempt == 2 {
			select {
			case <-sendLive:
			case <-ctx.Done():
				return
			}
			if websocket.Message.Send(ws, append([]byte{1}, live...)) != nil {
				return
			}
		}
		var input []byte
		_ = websocket.Message.Receive(ws, &input)
	}))
	defer func() { cancel(); server.Close() }()
	target := command.ConnectExecution{
		WorkID:   "work",
		Endpoint: server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString(),
	}
	seeded := nativeprocess.NewProtocolState()
	seeded.Offset, seeded.Ready, seeded.Active = 431, true, true
	replicas.seed(target, seeded)
	records := &nativeRecords{}
	delivery := newTestDelivery(t, records, records)
	managers := 0
	newManager := func() *dispatcher.NativeExecutions {
		managers++
		return replicas.replica(fmt.Sprintf("replica-%d", managers), records, delivery)
	}
	first := newManager()
	require.NoError(t, first.Connect(ctx, target))
	require.Eventually(t, func() bool {
		_, err := first.GetCurrentTurn(ctx, target.WorkID)
		return err == nil
	}, 3*time.Second, 10*time.Millisecond, "expired replay must leave a queryable connection")
	require.NoError(t, first.Shutdown(ctx))
	require.Equal(t, int64(431), replicas.stored(target).Offset, "never snapshot the middle of the discarded JSON line")

	second := newManager()
	require.NoError(t, second.Connect(ctx, target))
	require.Eventually(t, func() bool {
		turn, err := second.GetCurrentTurn(ctx, target.WorkID)
		return err == nil && turn.Active && turn.ID != ""
	}, 3*time.Second, 10*time.Millisecond, "retained replay must reach the real turn projection")
	select {
	case sendLive <- struct{}{}:
	case <-ctx.Done():
		t.Fatal("recovered attachment did not wait for live output")
	}
	require.Eventually(t, func() bool {
		turn, err := second.GetCurrentTurn(ctx, target.WorkID)
		return err == nil && !turn.Active
	}, 3*time.Second, 10*time.Millisecond, "live result must arrive over the same recovered connection")
	require.NoError(t, second.Shutdown(ctx))
	require.Equal(t, int64(900+len(fragment)+len(retained)+len(live)), replicas.stored(target).Offset)
	require.Equal(t, int32(2), attachments.Load(), "one attach per recovery, no retry with a guessed cursor")
	require.Equal(t, "431", <-attempts)
	require.Equal(t, "431", <-attempts)
	require.Eventually(t, func() bool { records.mu.Lock(); defer records.mu.Unlock(); return len(records.messages) >= 2 }, 3*time.Second, 10*time.Millisecond)
	records.mu.Lock()
	defer records.mu.Unlock()
	require.Len(t, records.messages, 2, "discarded fragment must not become a stored result")
	require.NotNil(t, records.messages[0].GetMessage())
	require.Equal(t, "live answer", records.messages[1].GetResult().GetText())
}
