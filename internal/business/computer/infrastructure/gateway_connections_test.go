package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/infrastructure"
)

// A Computer holds a connection exactly while the Gateway's record of it
// exists; an expired record holds none.
func TestGatewayConnectionsFollowThePublishedRecord(t *testing.T) {
	server := miniredis.RunT(t)
	connections := infrastructure.NewGatewayConnections(goredis.NewClient(&goredis.Options{Addr: server.Addr()}))
	server.HSet("computer:connection:c", "connection_id", "one", "replica", "a", "endpoint", "192.0.2.1:8085")
	server.SetTTL("computer:connection:c", time.Minute)

	connected, err := connections.Connected(context.Background(), []string{"c", "other"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"c": true, "other": false}, connected)

	server.FastForward(time.Minute + time.Second)
	connected, err = connections.Connected(context.Background(), []string{"c"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"c": false}, connected)
}
