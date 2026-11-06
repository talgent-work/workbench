package computerconnection

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestTheNewestConnectionHoldsTheRecord(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	ctx := context.Background()
	writer, reader := NewWriter(rdb), NewReader(rdb)
	first := Connection{ID: "one", Replica: "a", Endpoint: "192.0.2.1:8085"}
	second := Connection{ID: "two", Replica: "b", Endpoint: "192.0.2.2:8085"}

	require.NoError(t, writer.Claim(ctx, "c", first))
	held, err := writer.Refresh(ctx, "c", first)
	require.NoError(t, err)
	require.Equal(t, Held, held)

	require.NoError(t, writer.Claim(ctx, "c", second))
	replaced, err := writer.Refresh(ctx, "c", first)
	require.NoError(t, err)
	require.Equal(t, Replaced, replaced)
	released, err := writer.Release(ctx, "c", first.ID)
	require.NoError(t, err)
	require.False(t, released, "an older connection never removes the newer one's record")
	got, ok, err := reader.Lookup(ctx, "c")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, second, got)

	server.FastForward(TTL + 1)
	connected, err := reader.Connected(ctx, []string{"c", "other"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"c": false, "other": false}, connected)
	reclaimed, err := writer.Refresh(ctx, "c", second)
	require.NoError(t, err)
	require.Equal(t, Reclaimed, reclaimed)

	released, err = writer.Release(ctx, "c", second.ID)
	require.NoError(t, err)
	require.True(t, released)
	_, ok, err = reader.Lookup(ctx, "c")
	require.NoError(t, err)
	require.False(t, ok)
}
