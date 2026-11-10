package connectserver

import (
	"context"
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
