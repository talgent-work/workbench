package connectserver

import (
	"context"
	"net/http"
	"regexp"
	"runtime/debug"
	"sync"

	"connectrpc.com/connect/v2"
)

// ContractsVersionHeader names, on every response, the release of the
// contracts module the Workbench was built with, so a consumer can refuse a
// Workbench older than the contracts it was built with.
const ContractsVersionHeader = "talgent-contracts-version"

const contractsModule = "github.com/talgent-work/contracts"

// release is a release tag: no pre-release, pseudo-version or build suffix.
var release = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// BuiltContractsVersion is the contracts release of this binary, or "" when
// its build names none.
var BuiltContractsVersion = sync.OnceValue(func() string {
	info, _ := debug.ReadBuildInfo()
	return ContractsVersion(info)
})

// ContractsVersion is the release of the contracts module info names, or ""
// when it names none: a pseudo-version, a replacement by a directory or a
// build without the module.
func ContractsVersion(info *debug.BuildInfo) string {
	if info == nil {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path != contractsModule {
			continue
		}
		if dep.Replace != nil {
			dep = dep.Replace
		}
		if release.MatchString(dep.Version) {
			return dep.Version
		}
		return ""
	}
	return ""
}

// NewContractsVersionInterceptor answers every call with version in
// ContractsVersionHeader, before the handler runs, so refusals carry it too.
// An empty version sends no header.
func NewContractsVersionInterceptor(version string) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		if version == "" {
			return next
		}
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if info, ok := connect.CallInfoForServerContext(ctx); ok {
				info.ResponseHeader().Set(ContractsVersionHeader, version)
			}
			return next(ctx, spec, stream)
		}
	}
}

// NewContractsVersionHandler answers every response of next with version in
// ContractsVersionHeader, for a listener that answers some requests outside
// a Connect server, such as a native API's own refusals. A response that
// already carries it, from a Connect server behind next, keeps its own. An
// empty version sends no header.
func NewContractsVersionHandler(version string, next http.Handler) http.Handler {
	if version == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&versionWriter{ResponseWriter: w, version: version}, r)
	})
}

// versionWriter adds the version header when the response's header is
// written, after the handler has set its own.
type versionWriter struct {
	http.ResponseWriter
	version string
	written bool
}

func (w *versionWriter) stamp() {
	if w.written {
		return
	}
	w.written = true
	if w.Header().Get(ContractsVersionHeader) == "" {
		w.Header().Set(ContractsVersionHeader, w.version)
	}
}

func (w *versionWriter) WriteHeader(code int) {
	w.stamp()
	w.ResponseWriter.WriteHeader(code)
}

func (w *versionWriter) Write(b []byte) (int, error) {
	w.stamp()
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming responses streaming.
func (w *versionWriter) Flush() {
	w.stamp()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *versionWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
