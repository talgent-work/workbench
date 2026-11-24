package execution

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Agent reads the process and endpoint Dispatcher published for a Work; a
// Work without a published process has no reading.
func TestDispatcherStreamsReadThePublishedRecord(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	streams := NewDispatcherStreams(rdb)

	_, found, err := streams.Reading(ctx, "work")
	require.NoError(t, err)
	require.False(t, found)

	server.HSet("dispatcher:owner:work", "process_id", "process", "endpoint", "http://sandbox:44772", "instance", "a")
	reading, found, err := streams.Reading(ctx, "work")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StreamReading{ProcessID: "process", Endpoint: "http://sandbox:44772"}, reading)
}
