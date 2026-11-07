// Package connectserver builds the Workbench's Connect servers: each answers
// with the contracts version and logs every call once, before the interceptors
// of its own services run.
package connectserver

import (
	"log/slog"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/talgent-work/workbench/internal/pkg/connectlog"
)

// New is a Connect server whose every call carries ContractsVersionHeader
// and is logged once; interceptors run inside them, in order.
func New(logger *slog.Logger, interceptors ...connect.ServerInterceptor) *connect.Server {
	shared := []connect.ServerInterceptor{NewContractsVersionInterceptor(BuiltContractsVersion()), connectlog.NewInterceptor(logger)}
	return connect.NewServer(append(shared, interceptors...)...)
}

// Mount serves server's procedures on mux. Messages are read whole, without
// connect's default 4 MiB bound, as before connect v2, unless options bound
// them.
func Mount(mux connecthttp.ServeMux, server *connect.Server, options ...connecthttp.Option) {
	connecthttp.Mount(mux, server, append([]connecthttp.Option{connecthttp.WithReadMaxBytes(0)}, options...)...)
}
