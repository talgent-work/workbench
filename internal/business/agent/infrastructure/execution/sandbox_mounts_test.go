package execution

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// testLayout is what the default Work Layout locates for a Work without a
// Subject or Project on a Cloud: its HOME and Session Workspace, its
// definition's Claude Code state and its own inputs.
func testLayout() domain.LocatedLayout {
	located := func(condition domain.LocatorCondition, value, name string, backend domain.DirectoryBackend, lifetime domain.DirectoryLifetime) domain.LocatedDirectory {
		return domain.LocatedDirectory{Directory: domain.Directory{Owner: []domain.OwnerFact{{Condition: condition, Value: value}}, Name: name}, Backend: backend, Lifetime: lifetime}
	}
	inputs := located(domain.ConditionWork, "session", "inputs", domain.BackendJuiceFS, domain.LifetimePermanent)
	return domain.LocatedLayout{
		Mounts: []domain.LocatedMount{
			{Path: "$HOME", Directory: located(domain.ConditionWork, "session", "home", domain.BackendNAS, domain.LifetimeWork)},
			{Path: "/workspace", Directory: located(domain.ConditionWork, "session", "workspace", domain.BackendNAS, domain.LifetimeWork)},
			{Path: "$HOME/.claude", Directory: located(domain.ConditionAgentDefinition, "agent", "claude-code", domain.BackendNAS, domain.LifetimePermanent)},
			{Path: "/workspace/inputs", ReadOnly: true, Directory: inputs},
		},
		Inputs:            &inputs,
		PublicationTarget: located(domain.ConditionWork, "session", "artifacts", domain.BackendJuiceFS, domain.LifetimePermanent),
	}
}

// testMounts are the Mount Grants Storage issues for request's located
// layout, in its order.
var testMounts = domain.SandboxMounts{
	{ClaimName: "nas-root", SubPath: "homes/key"},
	{ClaimName: "nas-root", SubPath: "workspaces/key"},
	{ClaimName: "nas-root", SubPath: "harness/key"},
	{ClaimName: "juicefs-inputs", SubPath: "talgent/inputs/key", ReadOnly: true},
}

// grantsFor are Mount Grants Storage issues for layout, in its order.
func grantsFor(layout domain.LocatedLayout) domain.SandboxMounts {
	grants := make(domain.SandboxMounts, 0, len(layout.Mounts))
	for i, mount := range layout.Mounts {
		grants = append(grants, domain.MountGrant{ClaimName: "nas-root", SubPath: fmt.Sprintf("directories/%d", i), ReadOnly: mount.ReadOnly})
	}
	return grants
}

// A sandbox mounts exactly the grants Storage issued for the located layout,
// one per located Directory: a layout that located no inputs mounts none.
func TestASandboxMountsOnlyWhatItsLayoutLocated(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	input.LocatedLayout.Mounts = input.LocatedLayout.Mounts[:3]
	spec, err := provider.specification(context.Background(), input, testMounts[:3], owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	require.Len(t, spec.Volumes, 3)
	for _, volume := range spec.Volumes {
		require.NotEqual(t, "/workspace/inputs", volume.MountPath)
	}
	_, err = provider.specification(context.Background(), input, testMounts, owner(input), "test-token", "work-credential-fixture")
	require.ErrorIs(t, err, domain.ErrExecutionRequest, "a grant for no located Directory fails the start")
}

// The Credential Proxy injects the Identity Token, the Work credential, and
// the Space Token when
// the Work has an input source, into the requests sent to the platform's
// native API, and to the Gateway where one is deployed, and nothing else;
// the sandbox never holds either.
func TestWorkCredentialsAreInjectedOnlyIntoPlatformRequests(t *testing.T) {
	identity := command.IdentityToken{Token: "agent-token", ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
	const api, gateway = "http://controlplane.talgent.svc.cluster.local", "http://connector.talgent.svc.cluster.local"
	plan, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api, gatewayAddressEnv: gateway, "OTHER": "http://other.example.com"}), identity, "space-token")
	require.NoError(t, err)
	require.Equal(t, []sdk.Credential{
		{Name: identityTokenVaultEntry, Source: sdk.InlineCredentialSource{Value: "Bearer agent-token"}},
		{Name: workIDVaultEntry, Source: sdk.InlineCredentialSource{Value: "work-1"}},
		{Name: workCredentialVaultEntry, Source: sdk.InlineCredentialSource{Value: "credential-1"}},
		{Name: spaceTokenVaultEntry, Source: sdk.InlineCredentialSource{Value: "space-token"}},
	}, plan.Credentials)
	require.Len(t, plan.Bindings, 2)
	hosts := map[string][]string{}
	for _, binding := range plan.Bindings {
		hosts[binding.Name] = binding.Match.Hosts
		require.Equal(t, []sdk.CredentialScheme{sdk.CredentialSchemeHTTP}, binding.Match.Schemes)
		require.Equal(t, sdk.CredentialAuth{Type: sdk.CredentialAuthCustomHeaders, Headers: []sdk.CustomHeaderEntry{
			{Name: "Authorization", Credential: identityTokenVaultEntry},
			{Name: authn.HeaderNativeWorkID, Credential: workIDVaultEntry},
			{Name: authn.HeaderNativeCredential, Credential: workCredentialVaultEntry},
			{Name: authn.HeaderSpaceToken, Credential: spaceTokenVaultEntry},
		}}, binding.Auth)
	}
	require.Equal(t, map[string][]string{platformAPIBinding: {"controlplane.talgent.svc.cluster.local"}, gatewayBinding: {"connector.talgent.svc.cluster.local"}}, hosts)
	// The Vault reads a binding back normalized; the write is confirmed only
	// when the plan already says what the readback will.
	readback := sdk.CredentialVaultState{Revision: 1, Credentials: []sdk.CredentialMetadata{
		{Name: identityTokenVaultEntry, SourceType: "inline"}, {Name: workIDVaultEntry, SourceType: "inline"}, {Name: workCredentialVaultEntry, SourceType: "inline"}, {Name: spaceTokenVaultEntry, SourceType: "inline"},
	}}
	for _, binding := range plan.Bindings {
		normalized := binding.Match
		normalized.Methods, normalized.Paths = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, []string{"/*"}
		readback.Bindings = append(readback.Bindings, sdk.CredentialBindingMetadata{Name: binding.Name, Match: &normalized, Auth: &sdk.CredentialAuthMetadata{Type: string(sdk.CredentialAuthCustomHeaders)}})
	}
	require.True(t, vaultMatches(&readback, plan, nil), "the Vault's normalized readback confirms the write")

	alone, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api}), identity, "")
	require.NoError(t, err)
	require.Len(t, alone.Credentials, 3, "a Work without an input source has no Space Token")
	require.Len(t, alone.Bindings, 1, "without a Gateway only the native API receives the credential")
	_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("", map[string]string{platformAPIAddressEnv: api}), identity, "")
	require.Error(t, err)
	viaGateway, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: "", gatewayAddressEnv: gateway}), identity, "")
	require.NoError(t, err)
	require.Len(t, viaGateway.Bindings, 1, "a Work reaching the platform through the Gateway alone gives only it the credential")
	require.Equal(t, gatewayBinding, viaGateway.Bindings[0].Name)
	_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", nil), identity, "")
	require.Error(t, err, "every Work reaches the platform")
	_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api}), command.IdentityToken{}, "")
	require.Error(t, err, "no delivery reaches the platform without the Work's Identity Token")
	// The Vault accepts only fully qualified hosts, so a short Service name or
	// an address is refused here rather than by every credential delivery.
	for _, address := range []string{"http://controlplane", "http://10.43.0.5", "http://controlplane.talgent.svc.cluster.local:8081", "ftp://controlplane.talgent.svc.cluster.local", "http://controlplane.talgent.svc.cluster.local/api"} {
		_, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: address}), identity, "")
		require.Error(t, err, address)
		_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api, gatewayAddressEnv: address}), identity, "")
		require.Error(t, err, address)
	}
}

// sandboxEnvironment is a recorded sandbox environment of Work "work-1"
// holding credential and the platform addresses.
func sandboxEnvironment(credential string, addresses map[string]string) map[string]string {
	environment := map[string]string{"TALGENT_WORK_ID": "work-1", workCredentialEnv: credential}
	for key, value := range addresses {
		environment[key] = value
	}
	return environment
}

// The sandbox receives its specification without the Work credential; the
// platform keeps it to verify the sandbox's calls.
func TestTheSandboxNeverHoldsItsWorkCredential(t *testing.T) {
	spec := sdk.CreateSandboxRequest{Env: map[string]string{workCredentialEnv: "secret", "TALGENT_WORK_ID": "work-1"}}
	sent := withoutWorkCredential(spec)
	require.NotContains(t, sent.Env, workCredentialEnv)
	require.Equal(t, "work-1", sent.Env["TALGENT_WORK_ID"])
	require.Equal(t, "secret", spec.Env[workCredentialEnv], "the recorded specification keeps it")
}

// A missing or mis-scoped grant fails the start instead of being repaired.
func TestSandboxRefusesMissingOrMisScopedGrants(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	for name, change := range map[string]func(domain.SandboxMounts) domain.SandboxMounts{
		"no inputs grant":        func(m domain.SandboxMounts) domain.SandboxMounts { return m[:3] },
		"writable inputs":        func(m domain.SandboxMounts) domain.SandboxMounts { m[3].ReadOnly = false; return m },
		"read-only workspace":    func(m domain.SandboxMounts) domain.SandboxMounts { m[1].ReadOnly = true; return m },
		"no claim":               func(m domain.SandboxMounts) domain.SandboxMounts { m[0].ClaimName = ""; return m },
		"escaping subPath":       func(m domain.SandboxMounts) domain.SandboxMounts { m[1].SubPath = "../other"; return m },
		"absolute subPath":       func(m domain.SandboxMounts) domain.SandboxMounts { m[0].SubPath = "/homes/key"; return m },
		"unclean inputs subPath": func(m domain.SandboxMounts) domain.SandboxMounts { m[3].SubPath = "talgent/assets/../homes"; return m },
		"whole claim as a mount": func(m domain.SandboxMounts) domain.SandboxMounts { m[1].SubPath = "."; return m },
	} {
		input := request()
		mounts := change(append(domain.SandboxMounts(nil), testMounts...))
		_, err := provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
		require.ErrorIs(t, err, domain.ErrExecutionRequest, name)
	}
}

// The stored start keeps the located layout, so every later step mounts and
// publishes where the Work located at creation.
func TestStoredStartKeepsItsLocatedLayout(t *testing.T) {
	encoded, err := encodeLaunch(request())
	require.NoError(t, err)
	decoded, err := decodeStartRequest(encoded)
	require.NoError(t, err)
	require.Equal(t, testLayout(), decoded.LocatedLayout)
	require.Equal(t, "claude-code", decoded.AgentHarness)
}

// A platform address the Credential Proxy cannot match is refused at start,
// before any Work fails on it. A sandbox reaches the platform through its
// native API, through the Gateway where one is deployed, or both.
func TestSandboxConfigRefusesAnUninjectablePlatformAddress(t *testing.T) {
	for name, environment := range map[string]map[string]string{
		"native API alone":         {platformAPIAddressEnv: "http://controlplane.talgent.svc.cluster.local"},
		"Gateway alone":            {platformAPIAddressEnv: "", gatewayAddressEnv: "http://connector.talgent.svc.cluster.local"},
		"native API and a Gateway": {platformAPIAddressEnv: "http://controlplane.talgent.svc.cluster.local", gatewayAddressEnv: "http://connector.talgent.svc.cluster.local"},
	} {
		valid := config()
		valid.Environment = environment
		require.NoError(t, valid.Validate(), name)
	}
	for name, environment := range map[string]map[string]string{
		"no platform address":   {platformAPIAddressEnv: ""},
		"short native API host": {platformAPIAddressEnv: "http://controlplane"},
		"short Gateway host":    {platformAPIAddressEnv: "http://controlplane.talgent.svc.cluster.local", gatewayAddressEnv: "http://connector"},
	} {
		invalid := config()
		invalid.Environment = environment
		require.Error(t, invalid.Validate(), name)
	}
}

// The sandbox's egress reaches every platform address it was given: the
// native API, the Gateway where one is deployed, and the telemetry
// collector; every other private address stays denied.
func TestSandboxEgressReachesThePlatformAddresses(t *testing.T) {
	addresses := map[string]netip.Addr{
		"controlplane.talgent.svc.cluster.local":   netip.MustParseAddr("10.43.0.21"),
		"connector.talgent.svc.cluster.local":      netip.MustParseAddr("10.43.0.22"),
		"otel-collector.talgent.svc.cluster.local": netip.MustParseAddr("10.43.0.23"),
	}
	resolve := lookupHost
	lookupHost = func(_ context.Context, host string) ([]netip.Addr, error) { return []netip.Addr{addresses[host]}, nil }
	t.Cleanup(func() { lookupHost = resolve })
	denied := func(environment map[string]string) func(netip.Addr) bool {
		sandboxes := &Sandboxes{config: SandboxConfig{Environment: environment}}
		policy, err := sandboxes.networkPolicy(context.Background())
		require.NoError(t, err)
		return func(ip netip.Addr) bool {
			for _, rule := range policy.Egress {
				if prefix, err := netip.ParsePrefix(rule.Target); err == nil && rule.Action == "deny" && prefix.Contains(ip) {
					return true
				}
			}
			return false
		}
	}
	withGateway := denied(map[string]string{platformAPIAddressEnv: "http://controlplane.talgent.svc.cluster.local", gatewayAddressEnv: "http://connector.talgent.svc.cluster.local", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector.talgent.svc.cluster.local:4318"})
	for host, ip := range addresses {
		require.False(t, withGateway(ip), host)
	}
	require.True(t, withGateway(netip.MustParseAddr("10.43.0.24")), "another Service stays unreachable")
	alone := denied(map[string]string{platformAPIAddressEnv: "http://controlplane.talgent.svc.cluster.local"})
	require.False(t, alone(addresses["controlplane.talgent.svc.cluster.local"]))
	require.True(t, alone(addresses["connector.talgent.svc.cluster.local"]), "without a Gateway its address stays denied")
}
