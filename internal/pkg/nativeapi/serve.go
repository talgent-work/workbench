package nativeapi

import (
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/httpserver"
)

// Serve runs a process's native API on its own port, in front of the
// process's business handlers.
func Serve(lc fx.Lifecycle, cfg Config, business http.Handler, verifier authn.NativeProcessVerifier, calls Calls, reads ReadProcedures, logger *slog.Logger) error {
	handler, err := NewHandler(business, cfg.GatewayToken, verifier, calls, reads)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	httpserver.New(lc, httpserver.Config{Addr: cfg.Addr}, mux, logger)
	return nil
}
