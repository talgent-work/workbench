package trace

import (
	"log/slog"
	"net/http"
	"testing"

	"go.uber.org/fx"
)

// All native evidence and measurement queries must be wired to the shared
// Runtime-owned client, so adding a query cannot leave an unreachable RPC.
// The host process supplies the HTTP client, as controlplane does.
func TestTraceModuleWiring(t *testing.T) {
	err := fx.ValidateApp(Module, fx.NopLogger, fx.Supply(
		ClickHouseConfig{DSN: "clickhouse://localhost:9000/talgent_trace"},
		AccessConfig{ProjectAccessAddress: "http://controlplane:8080", AgentAddress: "http://controlplane:8080", AgentControlToken: "token", AccountAddress: "http://controlplane:8080"},
		http.NewServeMux(), slog.Default(), &http.Client{},
	))
	if err != nil {
		t.Fatal(err)
	}
}
