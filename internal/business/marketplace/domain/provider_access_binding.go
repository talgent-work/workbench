package domain

import (
	"reflect"
	"regexp"
)

// credentialPlaceholder is the public marker the Credential Proxy replaces
// with the target's credential, such as ${GITHUB_PERSONAL_ACCESS_TOKEN}.
var credentialPlaceholder = regexp.MustCompile(`\$\{[A-Z][A-Z0-9_]*\}`)

// ProviderAccessBinding contains verified service requirements, never a personal
// authorization reference. Identity is selected by the consuming Work.
type ProviderAccessBinding struct{ Provider, Target, Authentication string }

// ProviderCompatibility is the evidence that a configuration suits its
// Provider binding: the declared Provider target the binding names, for exactly
// the configuration it was checked against.
type ProviderCompatibility struct {
	Configuration MCPConfiguration
	Binding       ProviderAccessBinding
	Target        ProviderTarget
}

// validateProviderBinding checks what a retained Provider Connector keeps
// coherent by itself.
func validateProviderBinding(configuration MCPConfiguration, binding ProviderAccessBinding) error {
	if binding.Provider == "" || binding.Target == "" || binding.Authentication != "credential_proxy" {
		return ErrInvalidConnector
	}
	return configuration.Validate()
}

// validateProviderConfiguration admits a Provider Connector only with evidence
// for exactly its configuration: a remote server's address lies within the
// declared target, so the credential reaches no other host.
func validateProviderConfiguration(configuration MCPConfiguration, binding ProviderAccessBinding, evidence ProviderCompatibility) error {
	if !reflect.DeepEqual(configuration, evidence.Configuration) || binding != evidence.Binding || evidence.Target.Name != binding.Target {
		return ErrInvalidConnector
	}
	if configuration.Type == "http" && !evidence.Target.Contains(configuration.URL) {
		return ErrCredentialTargetInapplicable
	}
	// Credentials reach the server only through the proxy: every header and
	// environment value carries the placeholder, never a literal secret.
	for _, values := range []map[string]string{configuration.Headers, configuration.Environment} {
		for _, value := range values {
			if !credentialPlaceholder.MatchString(value) {
				return ErrCredentialTargetInapplicable
			}
		}
	}
	return validateProviderBinding(configuration, binding)
}
