package main

import (
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/nativeapi"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"
)

// registerNativeAPI serves the core's procedures an Agent reaches with its
// Work credential, directly or through the platform Connector: its owner's
// Library. It goes with the native API.
func registerNativeAPI(lc fx.Lifecycle, cfg nativeapi.Config, business *http.ServeMux, verifier authn.NativeProcessVerifier, live *nativeauth.LiveWorkClient, logger *slog.Logger) error {
	return nativeapi.Serve(lc, cfg, business, verifier,
		nativeapi.Calls{},
		nativeapi.ReadProcedures{Procedures: agent.AgentLibraryProcedures, Verifier: live}, logger)
}
