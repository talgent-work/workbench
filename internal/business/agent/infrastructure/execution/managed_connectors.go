package execution

import (
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Managed Work's Provider Connectors reach their targets through Talgent
// Computer's local proxy (Connector address at the local proxy): the Work's
// MCP configuration names the proxy's explicit address for each, and the
// proxy forwards it to the Connector's own address, adding the target
// credential delivered for it. No TLS is intercepted, and the credential
// stays out of the Work's processes, as its Identity Token does.

// connectorProxyPath is a Provider Connector's address below a run's proxy
// URL; its Connector identity names the delivered credential it takes.
func connectorProxyPath(connector domain.SelectedConnector) string {
	return "/connectors/" + connector.ID
}

// workConnectorCredential is one Provider Connector's entry in the relay's
// credentials PUT: where its proxy address forwards to, and the short-lived
// target credential added there. It carries a secret: it is never logged,
// recorded or echoed. A refresh token never reaches the machine.
type workConnectorCredential struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

// managedProviderConnectors are the retained Provider Connectors a Managed
// Work reaches through its local proxy.
func managedProviderConnectors(capabilities domain.CapabilitySnapshot) []domain.SelectedConnector {
	var connectors []domain.SelectedConnector
	for _, connector := range capabilities.Connectors {
		if connector.ProviderAuthorized() && connector.AvailableOnManaged() {
			connectors = append(connectors, connector)
		}
	}
	return connectors
}

// connectorCredentials pairs each of the Work's Provider Connectors with the
// supplied credential of its target. A Connector whose target has no current
// credential, or whose address does not lie on its target's declared hosts,
// is left out, so its proxy address answers that it is unavailable while the
// rest of the Work goes on.
func connectorCredentials(capabilities domain.CapabilitySnapshot, services []command.ServiceAccessMaterial) []workConnectorCredential {
	var credentials []workConnectorCredential
	for _, connector := range managedProviderConnectors(capabilities) {
		index := slices.IndexFunc(services, func(material command.ServiceAccessMaterial) bool {
			return material.Provider == connector.Provider && material.Target == connector.Target
		})
		if index < 0 {
			continue
		}
		material := services[index]
		address, err := credentialURL(connector.URL)
		if err != nil || material.Token == "" || !onTargetHosts(address.Hostname(), material.Hosts) {
			continue
		}
		credentials = append(credentials, workConnectorCredential{ID: connector.ID, URL: connector.URL, Token: material.Token})
	}
	return credentials
}

// onTargetHosts reports whether host lies on one of a target's declared host
// patterns: an exact host, or `*.` and a domain, which matches its
// subdomains.
func onTargetHosts(host string, declared []string) bool {
	patterns, err := targetHosts(declared)
	if err != nil {
		return false
	}
	host = strings.ToLower(host)
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		if domainName, wildcard := strings.CutPrefix(pattern, "*."); wildcard {
			return strings.HasSuffix(host, "."+domainName)
		}
		return host == pattern
	})
}

// managedAuthorizations lists, as a JSON array, the Provider Authorizations
// whose target credentials a Managed Work's Provider Connectors take, so a
// change Marketplace publishes to one replaces what the machine holds
// (Credential replacement).
func managedAuthorizations(request domain.LaunchSpecification) ([]byte, error) {
	providers := map[string]bool{}
	for _, connector := range managedProviderConnectors(request.Capabilities) {
		providers[connector.Provider] = true
	}
	selection := request.AuthorizationSelection.Clone()
	selection.Authorizations = slices.DeleteFunc(selection.Authorizations, func(authorization domain.WorkServiceAuthorization) bool {
		return !providers[authorization.Service]
	})
	return usedAuthorizations(selection)
}
