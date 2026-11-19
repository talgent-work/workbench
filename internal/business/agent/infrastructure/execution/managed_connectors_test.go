package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// Only a short-lived credential for a Connector whose address lies on its
// target's declared hosts is delivered; a refused one is observed as
// unavailable, with why.
func TestManagedConnectorCredentialIsShortLivedAndStaysOnItsTargetHosts(t *testing.T) {
	remote := connectorcatalogtest.Remote().SelectedConnector
	material := command.ServiceAccessMaterial{ServiceRequirement: command.ServiceRequirement{Provider: remote.Provider, Service: remote.Provider, Target: remote.Target},
		ServiceCredentialMaterial: command.ServiceCredentialMaterial{Token: "docs-access", ExpiresAt: time.Now().Add(time.Hour), Hosts: []string{"docs.example.com"}}}
	snapshot := domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{remote}}
	available := []command.ServiceAccessStatus{{Service: remote.Provider, Target: remote.Target, Status: "credential_available"}}
	for name, c := range map[string]struct {
		hosts     []string
		expiresAt time.Time
		reason    string
	}{
		"exact host":     {hosts: []string{"docs.example.com"}, expiresAt: material.ExpiresAt},
		"subdomains":     {hosts: []string{"*.example.com"}, expiresAt: material.ExpiresAt},
		"another domain": {hosts: []string{"*.example.org"}, expiresAt: material.ExpiresAt, reason: "outside_target_hosts"},
		"no expiry":      {hosts: []string{"docs.example.com"}, reason: "not_short_lived"},
	} {
		material.Hosts, material.ExpiresAt = c.hosts, c.expiresAt
		credentials, access := connectorCredentials(snapshot, []command.ServiceAccessMaterial{material}, available)
		if c.reason == "" {
			require.Equal(t, []workConnectorCredential{{ID: remote.ID, URL: remote.URL, Token: "docs-access"}}, credentials, name)
			require.Equal(t, available, access, name)
			continue
		}
		require.Empty(t, credentials, name)
		require.Equal(t, []command.ServiceAccessStatus{{Service: remote.Provider, Target: remote.Target, Status: "unavailable", Reason: c.reason}}, access, name)
	}
	require.Equal(t, "credential_available", available[0].Status, "the supplied statuses are not changed")
}

// A Managed execution uses the authorizations of its Provider Connectors'
// Providers alone, whatever service name an authorization carries.
func TestManagedExecutionUsesItsProviderConnectorsAuthorizations(t *testing.T) {
	request := domain.LaunchSpecification{Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Remote().SelectedConnector, connectorcatalogtest.GitHub().SelectedConnector}},
		AuthorizationSelection: domain.WorkAuthorizationSelection{OwnerID: "owner", Authorizations: []domain.WorkServiceAuthorization{
			{AuthorizationID: "01900000-0000-7000-8000-0000000005a1", Provider: "example-sso", Service: "docs-service"},
			{AuthorizationID: "01900000-0000-7000-8000-0000000005a2", Provider: "github", Service: "github"},
		}}}
	used, err := managedAuthorizations(request)
	require.NoError(t, err)
	require.JSONEq(t, `["01900000-0000-7000-8000-0000000005a1"]`, string(used), "the local command GitHub Connector is not served on the machine")
}
