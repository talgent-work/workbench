package streamgateway_test

import (
	"log/slog"
	"net/http"
	"testing"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/streamgateway"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// The module assembles from what cmd/streamgateway's runtime modules provide.
func TestModuleAssembles(t *testing.T) {
	err := fx.ValidateApp(
		fx.NopLogger,
		fx.Supply(slog.Default(), http.DefaultClient, streamgateway.AgentConfig{}),
		fx.Provide(func() connectrpc.Server { return connecttest.NewServer() }),
		fx.Provide(func() *pkgclickhouse.LazyConn { return nil }, func() *pkgnats.LazyConn { return nil }),
		streamgateway.Module,
	)
	if err != nil {
		t.Fatal(err)
	}
}
