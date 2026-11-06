package clickhouse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"
	"gopkg.in/yaml.v3"
)

// ClickHouse middleware config must deserialize the service DSN under the
// shared middleware package instead of cmd-local config structs.
func TestConfigYAMLUnmarshal(t *testing.T) {
	raw := []byte("dsn: clickhouse://127.0.0.1:9000/talgent_messages\n")

	var cfg Config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Equal(t, "clickhouse://127.0.0.1:9000/talgent_messages", cfg.DSN)
}

// NewConn must reject malformed DSNs at startup so services do not wire a
// broken eager ClickHouse connection.
func TestNewConnRejectsMalformedDSN(t *testing.T) {
	lc := fxtest.NewLifecycle(t)

	conn, err := NewConn(lc, Config{DSN: "://bad-dsn"})

	require.Error(t, err)
	require.Nil(t, conn)
}

// NewLazyConn must preserve degraded startup: malformed DSNs surface on first
// use, not when the service graph is built.
func TestNewLazyConnDefersMalformedDSNUntilFirstUse(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	client := NewLazyConn(lc, Config{DSN: "://bad-dsn"})

	lc.RequireStart()
	require.NotNil(t, client)
	conn, err := client.Conn(context.Background())
	require.Error(t, err)
	require.Nil(t, conn)
	lc.RequireStop()
}
