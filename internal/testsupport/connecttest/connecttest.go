// Package connecttest serves Connect services in tests.
package connecttest

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
)

// Handler serves, over HTTP, the services register registers on one Connect
// server with the interceptors.
func Handler(register func(*connect.Server), interceptors ...connect.ServerInterceptor) http.Handler {
	server := connect.NewServer(interceptors...)
	register(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	return mux
}
