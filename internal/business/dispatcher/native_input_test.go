package dispatcher

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

// A flush that cannot read the input buffer retries itself up to its ceiling,
// then gives up with an error; the input stays due for the Work's next
// trigger instead of being retried every second for the connection's life.
func TestAFlushThatCannotReadTheBufferGivesUpAtItsCeiling(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	var logs bytes.Buffer
	m := &NativeExecutions{buffer: dispatcherredis.NewInputBuffer(rdb), logger: slog.New(slog.NewJSONHandler(&logs, nil)), ctx: context.Background(), connections: map[string]*nativeExecution{}}
	e := &nativeExecution{target: command.ConnectExecution{WorkID: "work-1"}, ProtocolState: &nativeprocess.ProtocolState{Ready: true}, attached: true}
	for range flushRetries {
		require.NoError(t, m.flush(context.Background(), e))
		require.NotContains(t, logs.String(), "input buffer flush given up")
	}
	require.NoError(t, m.flush(context.Background(), e))
	require.Contains(t, logs.String(), `"level":"ERROR","msg":"input buffer flush given up"`)
	require.True(t, e.flushDue, "the input stays due for the next trigger")
}
