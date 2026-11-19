package domain

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Provider names an external party the deployment declares, such as github.
type Provider string

// ProviderGitHub is the Provider whose App installations, access scope and
// repository credentials the GitHub adapter serves beside its declaration.
const ProviderGitHub Provider = "github"

var ErrInvalidProviderDeclaration = errors.New("marketplace: invalid Provider declaration")

// TargetCredentialDerivation is how a Provider target's credential derives
// from one authorization.
type TargetCredentialDerivation string

const (
	// SharedTargetCredential serves every target with the authorization's own
	// credential.
	SharedTargetCredential TargetCredentialDerivation = "shared"
	// PerTargetCredential obtains a distinct credential for each target from
	// the authorization; how is the Provider adapter's.
	PerTargetCredential TargetCredentialDerivation = "per-target"
)

var (
	providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	hostLabel    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// HostPattern is an exact host name, or `*.` and a domain of at least two
// labels for any of its subdomains.
type HostPattern string

func (p HostPattern) valid() bool {
	host, wildcard := strings.CutPrefix(string(p), "*.")
	labels := strings.Split(host, ".")
	if wildcard && len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// Matches reports whether host lies within the pattern; a wildcard never
// matches its own domain.
func (p HostPattern) Matches(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if domain, wildcard := strings.CutPrefix(string(p), "*."); wildcard {
		return strings.HasSuffix(host, "."+domain) && host != domain
	}
	return host == string(p)
}

// ProviderTarget is one destination a Provider declares for the credentials
// of its authorization: the hosts they reach.
type ProviderTarget struct {
	Name  string
	Hosts []HostPattern
}

// Contains reports whether an HTTPS address lies on one of the target's hosts.
func (t ProviderTarget) Contains(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	return slices.ContainsFunc(t.Hosts, func(p HostPattern) bool { return p.Matches(u.Hostname()) })
}

// ProviderDeclaration is a Provider as the deployment declares it: the
// targets its one authorization serves, how each target's credential derives
// and whether target credentials expire. Its authorization server and client
// belong to the adapter that reaches it.
type ProviderDeclaration struct {
	Name              Provider
	DisplayName       string
	Targets           []ProviderTarget
	Derivation        TargetCredentialDerivation
	CredentialsExpire bool
	// ObservesAccessScope marks a Provider whose adapter observes the
	// accounts and repositories an authorization reaches.
	ObservesAccessScope bool
}

func (d ProviderDeclaration) Validate() error {
	if !providerName.MatchString(string(d.Name)) || strings.TrimSpace(d.DisplayName) == "" || len(d.Targets) == 0 {
		return ErrInvalidProviderDeclaration
	}
	if d.Derivation != SharedTargetCredential && d.Derivation != PerTargetCredential {
		return ErrInvalidProviderDeclaration
	}
	names := map[string]bool{}
	for _, target := range d.Targets {
		if !providerName.MatchString(target.Name) || names[target.Name] || len(target.Hosts) == 0 {
			return ErrInvalidProviderDeclaration
		}
		names[target.Name] = true
		for _, host := range target.Hosts {
			if !host.valid() {
				return ErrInvalidProviderDeclaration
			}
		}
	}
	return nil
}

// Target finds a declared target by name.
func (d ProviderDeclaration) Target(name string) (ProviderTarget, bool) {
	index := slices.IndexFunc(d.Targets, func(t ProviderTarget) bool { return t.Name == name })
	if index < 0 {
		return ProviderTarget{}, false
	}
	return d.Targets[index], true
}

// TargetNames are the names of every declared target.
func (d ProviderDeclaration) TargetNames() []string {
	names := make([]string, len(d.Targets))
	for i, target := range d.Targets {
		names[i] = target.Name
	}
	return names
}
