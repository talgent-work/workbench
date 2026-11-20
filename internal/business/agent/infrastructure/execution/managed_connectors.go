package execution

import (
	"net/url"
	"slices"

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

// connectorCredentials pairs each of the Work's Provider Connectors with the
// supplied credential of its target, and settles the observed access of each
// target it refuses. A Connector is left out, so its proxy address answers
// that it is unavailable while the rest of the Work goes on, when its target
// has no supplied credential, when that credential declares no expiry (only a
// short-lived target credential reaches the machine), or when its address does
// not lie on its target's declared hosts and paths.
func connectorCredentials(capabilities domain.CapabilitySnapshot, services []command.ServiceAccessMaterial, access []command.ServiceAccessStatus) ([]workConnectorCredential, []command.ServiceAccessStatus) {
	var credentials []workConnectorCredential
	access = slices.Clone(access)
	refuse := func(connector domain.SelectedConnector, reason string) {
		for n := range access {
			if access[n].Service == connector.Provider && access[n].Target == connector.Target && access[n].Status == "credential_available" {
				access[n].Status, access[n].Reason = "unavailable", reason
			}
		}
	}
	for _, connector := range capabilities.ManagedProviderConnectors() {
		index := slices.IndexFunc(services, func(material command.ServiceAccessMaterial) bool {
			return material.Provider == connector.Provider && material.Target == connector.Target
		})
		if index < 0 {
			continue
		}
		material := services[index]
		address, err := credentialURL(connector.URL)
		switch {
		case material.Token == "" || material.ExpiresAt.IsZero():
			refuse(connector, "not_short_lived")
		case err != nil || !onTarget(address, material.ServiceCredentialMaterial):
			refuse(connector, "outside_target")
		default:
			credentials = append(credentials, workConnectorCredential{ID: connector.ID, URL: connector.URL, Token: material.Token})
		}
	}
	return credentials, access
}

// onTarget reports whether an address lies on its target's declared hosts
// and paths; an undeclarable rule covers nothing.
func onTarget(address *url.URL, material command.ServiceCredentialMaterial) bool {
	rule, err := admitTarget(material)
	return err == nil && rule.covers(address)
}

// managedAuthorizations lists, as a JSON array, the Provider Authorizations
// whose target credentials a Managed Work's Provider Connectors take, so a
// change Marketplace publishes to one replaces what the machine holds
// (Credential replacement).
func managedAuthorizations(request domain.LaunchSpecification) ([]byte, error) {
	providers := map[string]bool{}
	for _, connector := range request.Capabilities.ManagedProviderConnectors() {
		providers[connector.Provider] = true
	}
	selection := request.AuthorizationSelection.Clone()
	selection.Authorizations = slices.DeleteFunc(selection.Authorizations, func(authorization domain.WorkServiceAuthorization) bool {
		return !providers[authorization.Provider]
	})
	return usedAuthorizations(selection)
}
