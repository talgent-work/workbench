package execution

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// Vault entries the Credential Proxy injects into every request to a platform
// address: the Work's Identity Token, its identity and credential, and the
// Space Token of its input source when it has one.
const (
	identityTokenVaultEntry  = "identity-token"
	workIDVaultEntry         = "work-id"
	workCredentialVaultEntry = "work-credential"
	spaceTokenVaultEntry     = "space-token"
	platformAPIBinding       = "talgent-api"
	gatewayBinding           = "talgent-gateway"
)

// The platform addresses a sandbox may reach, each named by the environment
// variable that tells its Agent where: the entry, and the platform
// Connector where the deployment runs one. A deployment sets at least one.
const (
	platformAPIAddressEnv = "TALGENT_API_ADDRESS"
	gatewayAddressEnv     = "TALGENT_GATEWAY_ADDRESS"
)

// platformAddress is one origin the Credential Proxy adds the Work's
// credentials to.
type platformAddress struct {
	binding string
	origin  *url.URL
}

// platformAddresses are the platform origins the environment sets.
func platformAddresses(environment map[string]string) ([]platformAddress, error) {
	var addresses []platformAddress
	for _, candidate := range []struct{ env, binding string }{{platformAPIAddressEnv, platformAPIBinding}, {gatewayAddressEnv, gatewayBinding}} {
		raw := environment[candidate.env]
		if raw == "" {
			continue
		}
		origin, err := platformOrigin(candidate.env, raw)
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, platformAddress{binding: candidate.binding, origin: origin})
	}
	if len(addresses) == 0 {
		return nil, errors.New("agent: " + platformAPIAddressEnv + " or " + gatewayAddressEnv + " must name where a sandbox reaches the platform")
	}
	return addresses, nil
}

// withPlatformCredentials adds the Work's platform identity to the Vault
// plan: its Identity Token as supplied, and its Work credential as the
// recorded sandbox environment holds it. Like every credential, none of it
// enters the workload: the Credential Proxy injects the Identity Token as
// `Authorization: Bearer`, and the Space Token when the Work has an input
// source, into the requests sent to the platform addresses and into nothing
// else; only the platform Connector also receives the Work credential.
func withPlatformCredentials(plan sdk.CredentialVaultCreateRequest, environment map[string]string, identity command.IdentityToken, spaceToken string) (sdk.CredentialVaultCreateRequest, error) {
	addresses, err := platformAddresses(environment)
	if err != nil {
		return sdk.CredentialVaultCreateRequest{}, err
	}
	workID, credential := environment["TALGENT_WORK_ID"], environment[workCredentialEnv]
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(credential) == "" {
		return sdk.CredentialVaultCreateRequest{}, errors.New("agent: a Work credential is required to reach the platform")
	}
	if strings.TrimSpace(identity.Token) == "" {
		return sdk.CredentialVaultCreateRequest{}, errors.New("agent: the Work's Identity Token is required to reach the platform")
	}
	plan.Credentials = append(plan.Credentials,
		sdk.Credential{Name: identityTokenVaultEntry, Source: sdk.InlineCredentialSource{Value: "Bearer " + identity.Token}},
		sdk.Credential{Name: workIDVaultEntry, Source: sdk.InlineCredentialSource{Value: workID}},
		sdk.Credential{Name: workCredentialVaultEntry, Source: sdk.InlineCredentialSource{Value: credential}})
	// The entry takes the Identity Token alone; the platform Connector still
	// takes the Work credential beside it.
	entry := []sdk.CustomHeaderEntry{{Name: "Authorization", Credential: identityTokenVaultEntry}}
	if spaceToken != "" {
		plan.Credentials = append(plan.Credentials, sdk.Credential{Name: spaceTokenVaultEntry, Source: sdk.InlineCredentialSource{Value: spaceToken}})
		entry = append(entry, sdk.CustomHeaderEntry{Name: authn.HeaderSpaceToken, Credential: spaceTokenVaultEntry})
	}
	connector := append(slices.Clone(entry), sdk.CustomHeaderEntry{Name: authn.HeaderNativeWorkID, Credential: workIDVaultEntry}, sdk.CustomHeaderEntry{Name: authn.HeaderNativeCredential, Credential: workCredentialVaultEntry})
	for _, address := range addresses {
		headers := entry
		if address.binding == gatewayBinding {
			headers = connector
		}
		plan.Bindings = append(plan.Bindings, sdk.CredentialBinding{Name: address.binding,
			Match: sdk.CredentialMatch{Schemes: []sdk.CredentialScheme{sdk.CredentialScheme(address.origin.Scheme)}, Hosts: []string{strings.ToLower(address.origin.Hostname())},
				// Spelled as the Vault normalizes them, so the readback that
				// confirms the write compares equal.
				Methods: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, Paths: []string{"/*"}},
			Auth: sdk.CredentialAuth{Type: sdk.CredentialAuthCustomHeaders, Headers: headers}})
	}
	return plan, nil
}

// platformOrigin is a platform address as the Credential Proxy matches it: a
// bare origin on the standard port its scheme implies, the only ports the
// proxy sees, named by a fully qualified host, the only kind of host a Vault
// binding accepts. name is the variable that holds it.
func platformOrigin(name, address string) (*url.URL, error) {
	invalid := errors.New("agent: " + name + " must be an origin with a fully qualified host on a standard port for the Work credential to be injected")
	u, err := url.Parse(address)
	if err != nil {
		return nil, invalid
	}
	host := u.Hostname()
	qualified := strings.Contains(strings.Trim(host, "."), ".") && net.ParseIP(host) == nil
	origin := (u.Scheme == "http" || u.Scheme == "https") && qualified && u.Port() == ""
	bare := u.User == nil && strings.Trim(u.Path, "/") == "" && u.RawQuery == "" && u.Fragment == ""
	if !origin || !bare {
		return nil, invalid
	}
	return u, nil
}
