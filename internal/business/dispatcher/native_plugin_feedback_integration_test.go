//go:build integration

package dispatcher_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"golang.org/x/net/websocket"
)

type interruptedPluginFeedback struct {
	*nativeRecords
	calls   atomic.Int32
	reports chan command.ExecutionObservation
}

func (r *interruptedPluginFeedback) ReportExecution(ctx context.Context, observation command.ExecutionObservation) error {
	if observation.Kind == command.ExecutionPluginLoadsObserved {
		// Count before reporting: the test reads the count once it has
		// received the report.
		n := r.calls.Add(1)
		r.reports <- observation
		if n == 1 {
			return errors.New("injected feedback interruption")
		}
	}
	return r.nativeRecords.ReportExecution(ctx, observation)
}

// Intent (real): an interrupted feedback delivery is retried from the Work's
// feedback queue; reading the stream neither stops nor replays for it.
func TestPluginLoadingFeedbackIsRetriedWithoutStoppingTheStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	line := `{"type":"system","subtype":"init","plugins":[{"name":"runtime","version":"1"}]}` + "\n"
	offsets := make(chan string, 2)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		offsets <- ws.Request().URL.Query().Get("since")
		if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		if websocket.Message.Send(ws, append([]byte{1}, line...)) != nil {
			return
		}
		var data []byte
		_ = websocket.Message.Receive(ws, &data)
	}))
	defer server.Close()
	target := command.ConnectExecution{WorkID: "plugin-feedback", ProcessID: "process", NativeSessionID: uuid.NewString(), Endpoint: server.URL, AccessToken: "fixture"}
	replicas := newNativeReplicas(t)
	ready := nativeprocess.NewProtocolState()
	ready.Ready = true
	replicas.seed(target, ready)
	records := &nativeRecords{}
	feedback := &interruptedPluginFeedback{nativeRecords: records, reports: make(chan command.ExecutionObservation, 2)}
	manager := replicas.replica("replica", feedback, newTestDelivery(t, records, records))
	require.NoError(t, manager.Connect(ctx, target))
	for attempt := range 2 {
		select {
		case observation := <-feedback.reports:
			require.Equal(t, []command.LoadedPlugin{{Slug: "runtime", Version: "1"}}, observation.LoadedPlugins, "attempt %d", attempt)
		case <-ctx.Done():
			t.Fatal("loading feedback was not retried")
		}
	}
	turn, err := manager.GetCurrentTurn(ctx, target.WorkID)
	require.NoError(t, err, "the stream is still read")
	require.False(t, turn.Active)
	require.EqualValues(t, 2, feedback.calls.Load())
	require.Equal(t, "0", <-offsets)
	require.Empty(t, offsets, "no reconnect or replay")
}

func (*interruptedPluginFeedback) ReportConnectionEnded(context.Context, string, string) error {
	return nil
}
