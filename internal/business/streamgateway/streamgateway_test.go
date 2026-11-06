package streamgateway_test

import (
	"log/slog"
	"net/http"
	"testing"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/streamgateway"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
)

// The module assembles from what cmd/streamgateway's runtime modules provide.
func TestModuleAssembles(t *testing.T) {
	err := fx.ValidateApp(
		fx.NopLogger,
		fx.Supply(slog.Default(), http.DefaultClient, http.NewServeMux(), streamgateway.AgentConfig{}),
		fx.Provide(func() *pkgclickhouse.LazyConn { return nil }, func() *pkgnats.LazyConn { return nil }),
		streamgateway.Module,
	)
	if err != nil {
		t.Fatal(err)
	}
}
