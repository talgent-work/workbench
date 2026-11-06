package main

import (
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/storage"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/nativeapi"
	"github.com/talgent-work/workbench/pkg/nativeauth"
)

// registerNativeAPI serves the core's procedures an Agent reaches with its
// Work credential, directly or through the platform Connector: Artifacts of
// its Work and its owner's Library.
func registerNativeAPI(lc fx.Lifecycle, cfg nativeapi.Config, business *http.ServeMux, verifier authn.NativeProcessVerifier, live *nativeauth.LiveWorkClient, logger *slog.Logger) error {
	return nativeapi.Serve(lc, cfg, business, verifier,
		nativeapi.Calls{SpaceTokenProcedures: storage.AgentArtifactProcedures},
		nativeapi.ReadProcedures{Procedures: agent.AgentLibraryProcedures, Verifier: live}, logger)
}
