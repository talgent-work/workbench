// Package middlewaretest exposes fixtures owned by the test invocation.
package middlewaretest

import (
	"context"
	"os"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func Environment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	require.NotEmpty(t, value, "run through scripts/test/with-middleware.sh")
	return value
}

// redisDatabases is how many logical databases a Redis server has by default.
const redisDatabases = 16

// RedisDB leases one logical database of the shared Redis, emptied, and returns
// the server's address and the database's index. Each test holds its own, so
// packages run their Redis cases side by side instead of queueing for one.
func RedisDB(t *testing.T) (string, int) {
	t.Helper()
	address := Environment(t, "TALGENT_TEST_REDIS_ADDR")
	db, release := LeaseSlot(t, "redis-db", redisDatabases)
	t.Cleanup(release)
	client := redis.NewClient(&redis.Options{Addr: address, DB: db})
	require.NoError(t, client.FlushDB(t.Context()).Err())
	require.NoError(t, client.Close())
	return address, db
}

func ClickHouse(t *testing.T, database string) string {
	t.Helper()
	dsn := Environment(t, "TALGENT_TEST_CLICKHOUSE_DSN")
	unlock := Lock(t, "clickhouse-"+database)
	t.Cleanup(unlock)
	opts, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(opts)
	require.NoError(t, err)
	defer conn.Close()
	tables := []string{"talgent_messages.unified_message"}
	if database == "talgent_trace" {
		tables = []string{"talgent_trace.otel_traces", "talgent_trace.otel_traces_trace_id_ts", "talgent_trace.otel_logs", "talgent_trace.trace_work_measurements"}
	}
	for _, table := range tables {
		require.NoError(t, conn.Exec(context.Background(), "TRUNCATE TABLE "+table))
	}
	return dsn
}

func NATS(t *testing.T) string {
	t.Helper()
	url := Environment(t, "TALGENT_TEST_NATS_URL")
	unlock := Lock(t, "nats")
	t.Cleanup(unlock)
	return url
}
