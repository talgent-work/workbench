package execution

import (
	"errors"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// vaultMethods and vaultPaths are the official Vault's explicit defaults: a
// target declaring no methods or paths takes them, since omitted lists are
// expanded on write, which would otherwise fail our exact binding readback
// before the Work can start.
var (
	vaultMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	vaultPaths   = []string{"/*"}
)

// targetRule is where a supplied target credential may go, as Marketplace
// declared it (marketplace.private.v1 SupplyServiceCredentialResponse):
// host patterns, path patterns and methods, the latter two defaulted to
// every path and method.
type targetRule struct {
	hosts, paths, methods []string
}

// admitTarget admits a target's declared rule: an exact host or `*.` and a
// domain of at least two labels, never a bare wildcard that would send the
// credential anywhere; an exact clean path or one ending in `/*`; methods
// among the Vault's.
func admitTarget(material command.ServiceCredentialMaterial) (targetRule, error) {
	if len(material.Hosts) == 0 {
		return targetRule{}, errors.New("agent: a service credential needs its target hosts")
	}
	rule := targetRule{hosts: make([]string, len(material.Hosts)), paths: slices.Clone(material.Paths), methods: slices.Clone(material.Methods)}
	for i, host := range material.Hosts {
		host = strings.ToLower(host)
		bare := strings.TrimPrefix(host, "*.")
		if bare == "" || strings.ContainsAny(bare, "*/:@ ") || (bare != host && !strings.Contains(bare, ".")) || strings.HasPrefix(bare, ".") || strings.HasSuffix(bare, ".") {
			return targetRule{}, errors.New("agent: invalid service credential target host")
		}
		rule.hosts[i] = host
	}
	if len(rule.paths) == 0 {
		rule.paths = slices.Clone(vaultPaths)
	}
	for _, pattern := range rule.paths {
		exact := strings.TrimSuffix(pattern, "/*")
		trimmed := strings.TrimSuffix(exact, "/")
		if !strings.HasPrefix(pattern, "/") || strings.ContainsAny(exact, "*?%#\\ ") || (trimmed != "" && path.Clean(trimmed) != trimmed) {
			return targetRule{}, errors.New("agent: invalid service credential target path")
		}
	}
	if len(rule.methods) == 0 {
		rule.methods = slices.Clone(vaultMethods)
	}
	for _, method := range rule.methods {
		if !slices.Contains(vaultMethods, method) {
			return targetRule{}, errors.New("agent: invalid service credential target method")
		}
	}
	return rule, nil
}

// covers reports whether an address lies on one of the rule's hosts, where
// `*.` and a domain matches its subdomains, and on one of its paths, where
// a pattern ending in `/*` matches every path beneath it.
func (r targetRule) covers(address *url.URL) bool {
	host, requestPath := strings.ToLower(address.Hostname()), address.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	return slices.ContainsFunc(r.hosts, func(pattern string) bool {
		if domainName, wildcard := strings.CutPrefix(pattern, "*."); wildcard {
			return strings.HasSuffix(host, "."+domainName)
		}
		return host == pattern
	}) && slices.ContainsFunc(r.paths, func(pattern string) bool {
		if prefix, beneath := strings.CutSuffix(pattern, "/*"); beneath {
			return strings.HasPrefix(requestPath, prefix+"/")
		}
		return requestPath == pattern
	})
}
