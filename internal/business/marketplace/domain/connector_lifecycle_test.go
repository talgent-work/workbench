package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const (
	alice = "e6c7e756-007d-5236-a492-7f4b9be0b2e8"
	bob   = "cfe58e71-efc6-5336-8009-449ad568c42b"
)

var acmeSSO = domain.ProviderDeclaration{Name: "acme-sso", DisplayName: "Acme SSO", Derivation: domain.PerTargetCredential,
	Targets: []domain.ProviderTarget{{Name: "corp", Hosts: []domain.HostPattern{"*.corp.acme.example"}}}}

func remote(url string) domain.MCPConfiguration {
	return domain.MCPConfiguration{Type: "http", URL: url}
}

func addCRM(t *testing.T) *domain.Connector {
	t.Helper()
	connector, err := domain.AddConnector(domain.ConnectorOwner{Kind: domain.OwnerPerson, ID: alice}, domain.ConnectorAddition{
		Name: "acme-crm", Configuration: remote("https://crm.corp.acme.example/mcp"), Provider: &acmeSSO,
		Presentation: domain.CatalogPresentation{Description: "Customers", Category: "Sales"}}, []string{"talgent", "github"})
	require.NoError(t, err)
	return connector
}

// An added Connector is Private: only its owner sees and installs it, and it
// signs in through the target of its Provider that holds its address.
func TestAddedConnectorIsPrivateToItsOwner(t *testing.T) {
	connector := addCRM(t)
	require.Equal(t, domain.ConnectorPrivate, connector.State)
	require.Equal(t, domain.ProviderAuthorizationAuthentication, connector.Authentication)
	require.Equal(t, domain.ProviderAccessBinding{Provider: "acme-sso", Target: "corp", Authentication: "credential_proxy"}, *connector.ProviderAccessBinding)
	require.True(t, connector.InCatalogOf(alice))
	require.False(t, connector.InCatalogOf(bob))
	require.NoError(t, connector.AdmitInstallation(alice))
	require.ErrorIs(t, connector.AdmitInstallation(bob), domain.ErrConnectorNotInstallable)
}

func TestAddValidatesNameConfigurationAndProviderTargets(t *testing.T) {
	owner := domain.ConnectorOwner{Kind: domain.OwnerApplication, ID: "0193f6a2-7c55-7c4d-9a8e-3b5c1d2e4f60"}
	cases := map[string]struct {
		addition domain.ConnectorAddition
		err      error
	}{
		"reserved platform name":   {domain.ConnectorAddition{Name: "github", Configuration: remote("https://x.example/mcp")}, domain.ErrReservedConnectorName},
		"invalid name":             {domain.ConnectorAddition{Name: "Acme CRM", Configuration: remote("https://x.example/mcp")}, domain.ErrInvalidConnector},
		"address outside targets":  {domain.ConnectorAddition{Name: "leaky", Configuration: remote("https://crm.attacker.test/mcp"), Provider: &acmeSSO}, domain.ErrCredentialTargetInapplicable},
		"plain http to a Provider": {domain.ConnectorAddition{Name: "plain", Configuration: remote("http://crm.corp.acme.example/mcp"), Provider: &acmeSSO}, domain.ErrCredentialTargetInapplicable},
		"Provider local command":   {domain.ConnectorAddition{Name: "local", Configuration: domain.MCPConfiguration{Command: "npx"}, Provider: &acmeSSO}, domain.ErrProviderConnectorNotRemote},
		"headers":                  {domain.ConnectorAddition{Name: "headers", Configuration: domain.MCPConfiguration{Type: "http", URL: "https://x.example/mcp", Headers: map[string]string{"A": "b"}}}, domain.ErrInvalidConnector},
		"relative address":         {domain.ConnectorAddition{Name: "relative", Configuration: remote("/mcp")}, domain.ErrInvalidConnector},
		"oversized icon":           {domain.ConnectorAddition{Name: "icon", Configuration: remote("https://x.example/mcp"), Presentation: domain.CatalogPresentation{Icon: &domain.ConnectorIcon{MediaType: "image/png", Data: make([]byte, domain.MaxConnectorIconBytes+1)}}}, domain.ErrInvalidConnectorIcon},
		"icon of another type":     {domain.ConnectorAddition{Name: "icon", Configuration: remote("https://x.example/mcp"), Presentation: domain.CatalogPresentation{Icon: &domain.ConnectorIcon{MediaType: "image/png", Data: []byte("<svg/>")}}}, domain.ErrInvalidConnectorIcon},
	}
	for name, c := range cases {
		_, err := domain.AddConnector(owner, c.addition, []string{"talgent", "github"})
		require.ErrorIs(t, err, c.err, name)
	}
	// A credential-free Connector may be a local command or an HTTP server.
	local, err := domain.AddConnector(owner, domain.ConnectorAddition{Name: "chrome-devtools", Configuration: domain.MCPConfiguration{Command: "npx", Arguments: []string{"chrome-devtools-mcp@0.6.0"}},
		Presentation: domain.CatalogPresentation{Icon: &domain.ConnectorIcon{MediaType: "image/svg+xml", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}}}, nil)
	require.NoError(t, err)
	require.Equal(t, domain.NoAuthentication, local.Authentication)
	_, err = domain.AddConnector(owner, domain.ConnectorAddition{Name: "local-browser", Configuration: remote("http://localhost:9222/mcp")}, nil)
	require.NoError(t, err)
}

func TestOnlyTheOwnerChangesPublishesAndDeletes(t *testing.T) {
	connector := addCRM(t)
	now := time.Now()
	require.ErrorIs(t, connector.Change(bob, remote("https://crm.corp.acme.example/v2"), connector.Presentation, &acmeSSO), domain.ErrPermissionDenied)
	require.ErrorIs(t, connector.Publish(bob, now), domain.ErrPermissionDenied)
	require.ErrorIs(t, connector.Delete(bob, now), domain.ErrPermissionDenied)
	_, err := connector.Withdraw(domain.WithdrawingAuthority{Owner: bob})
	require.ErrorIs(t, err, domain.ErrPermissionDenied)

	require.ErrorIs(t, connector.Change(alice, remote("https://crm.attacker.test/mcp"), connector.Presentation, &acmeSSO), domain.ErrCredentialTargetInapplicable)
	require.Equal(t, "https://crm.corp.acme.example/mcp", connector.Configuration.URL, "a refused change keeps the Connector")
	require.NoError(t, connector.Change(alice, remote("https://crm2.corp.acme.example/mcp"), domain.CatalogPresentation{Description: "CRM", Category: "Sales"}, &acmeSSO))
	require.Equal(t, "acme-crm", connector.Name)
	require.Equal(t, "acme-sso", connector.ProviderAccessBinding.Provider, "the declared Provider stays")
	require.ErrorIs(t, connector.Change(alice, domain.MCPConfiguration{Type: "stdio", Command: "npx", Arguments: []string{"acme-crm@1.0.0"}}, connector.Presentation, &acmeSSO),
		domain.ErrInvalidConnector, "a remote server stays remote")
}

// Private → Published → Withdrawn, and Withdrawn is final; only a Private
// Connector is deleted, recording PrivateConnectorDeleted.
func TestConnectorLifecycleState(t *testing.T) {
	now := time.Now()
	connector := addCRM(t)
	_, err := connector.Withdraw(domain.WithdrawingAuthority{Owner: alice})
	require.ErrorIs(t, err, domain.ErrInvalidTransition, "a Private Connector is not withdrawn")
	require.NoError(t, connector.Publish(alice, now))
	require.Equal(t, domain.ConnectorPublished, connector.State)
	require.False(t, connector.PublishedAt.IsZero())
	require.True(t, connector.InCatalogOf(bob))
	require.ErrorIs(t, connector.Publish(alice, now), domain.ErrInvalidTransition)
	require.ErrorIs(t, connector.Delete(alice, now), domain.ErrInvalidTransition, "a published Connector is never deleted")

	withdrawn, err := connector.Withdraw(domain.WithdrawingAuthority{Operator: true})
	require.NoError(t, err)
	require.True(t, withdrawn)
	require.Equal(t, domain.ConnectorWithdrawn, connector.State)
	require.False(t, connector.InCatalogOf(alice), "a Withdrawn Connector is in no catalog, its owner's included")
	require.ErrorIs(t, connector.AdmitInstallation(bob), domain.ErrConnectorNotInstallable)
	require.ErrorIs(t, connector.Publish(alice, now), domain.ErrInvalidTransition, "withdrawal is final")
	require.ErrorIs(t, connector.Change(alice, connector.Configuration, connector.Presentation, &acmeSSO), domain.ErrInvalidTransition)
	again, err := connector.Withdraw(domain.WithdrawingAuthority{Owner: alice})
	require.NoError(t, err)
	require.False(t, again)

	private := addCRM(t)
	require.NoError(t, private.Delete(alice, now))
	events := private.Events()
	require.Len(t, events, 1)
	require.Equal(t, domain.PrivateConnectorDeleted{ConnectorID: private.ID, OwnerID: alice, OccurredAt: now.UTC()}, events[0])
	require.Empty(t, private.Events(), "events drain once")
}

func TestPlatformConnectorFollowsDeploymentConfigurationAlone(t *testing.T) {
	platform := githubConnector(t)
	_, err := platform.Withdraw(domain.WithdrawingAuthority{Operator: true})
	require.ErrorIs(t, err, domain.ErrInvalidTransition)
	require.ErrorIs(t, platform.Publish("", time.Now()), domain.ErrPermissionDenied)
	require.True(t, platform.InCatalogOf(bob))
}
