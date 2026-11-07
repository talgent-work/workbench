package mysql_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/talgent-work/workbench/internal/pkg/mysql"
)

func TestConfigYAMLUnmarshal(t *testing.T) {
	raw := []byte(`
dsn: "root:pw@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4"
max-open-conns: 20
max-idle-conns: 5
conn-max-lifetime: "30m"
conn-max-idle-time: "5m"
ping-timeout: "3s"
`)

	var cfg mysql.Config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Equal(t, "root:pw@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4", cfg.DSN)
	require.Equal(t, 20, cfg.MaxOpenConns)
	require.Equal(t, 5, cfg.MaxIdleConns)
	require.Equal(t, 30*time.Minute, cfg.ConnMaxLifetime.Duration())
	require.Equal(t, 5*time.Minute, cfg.ConnMaxIdleTime.Duration())
	require.Equal(t, 3*time.Second, cfg.PingTimeout.Duration())
}

func TestConfigJSONUnmarshal(t *testing.T) {
	raw := []byte(`{"dsn":"root:pw@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4","ping-timeout":"3s"}`)

	var cfg mysql.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Equal(t, "root:pw@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4", cfg.DSN)
	require.Equal(t, 3*time.Second, cfg.PingTimeout.Duration())
}
