package execution

import (
	"context"
	"net/netip"
	"testing"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/pkg/authn"
)

func withInputs(input domain.StartExecutionRequest) domain.StartExecutionRequest {
	input.InputSource = domain.WorkInputSource{Reference: "file-space/case-1", SpaceToken: "space-token"}
	return input
}

// inputMounts are Storage's grants for a start with an input source.
func inputMounts() domain.SandboxMounts {
	mounts := testMounts
	workspace := *testMounts.Workspace
	mounts.Workspace = &workspace
	mounts.Inputs = &domain.MountGrant{ClaimName: "juicefs-inputs", SubPath: "talgent/assets/spaces/case-1/attachments", ReadOnly: true}
	return mounts
}

// A sandbox mounts exactly the grants Storage issued: the workspace and HOME
// read-write and the input source's Attachments read-only at /workspace/inputs. The
// Space Token is a credential, so it never reaches the workload.
func TestSandboxMountsTheGrantsStorageIssued(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := withInputs(request())
	spec, err := provider.specification(context.Background(), input, inputMounts(), owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	mounts := map[string]sdk.Volume{}
	for _, volume := range spec.Volumes {
		mounts[volume.MountPath] = volume
	}
	require.Len(t, mounts, 3)
	require.Equal(t, "workspaces/key", mounts["/workspace"].SubPath)
	require.False(t, mounts["/workspace"].ReadOnly)
	require.Equal(t, "homes/key", mounts["/home/talgent"].SubPath)
	require.False(t, mounts["/home/talgent"].ReadOnly)
	inputs := mounts["/workspace/inputs"]
	require.True(t, inputs.ReadOnly, "a Work never changes its input source's Attachments")
	require.Equal(t, "juicefs-inputs", inputs.PVC.ClaimName)
	for name, value := range spec.Env {
		require.NotContains(t, value, "space-token", name)
	}
}

// The Credential Proxy injects the Work credential, and the Space Token when
// the Work has an input source, into the requests sent to the platform's
// native API, and to the Gateway where one is deployed, and nothing else;
// the sandbox never holds either.
func TestWorkCredentialsAreInjectedOnlyIntoPlatformRequests(t *testing.T) {
	const api, gateway = "http://controlplane.talgent.svc.cluster.local", "http://connector.talgent.svc.cluster.local"
	plan, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api, gatewayAddressEnv: gateway, "OTHER": "http://other.example.com"}), "space-token")
	require.NoError(t, err)
	require.Equal(t, []sdk.Credential{
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
			{Name: authn.HeaderNativeWorkID, Credential: workIDVaultEntry},
			{Name: authn.HeaderNativeCredential, Credential: workCredentialVaultEntry},
			{Name: authn.HeaderSpaceToken, Credential: spaceTokenVaultEntry},
		}}, binding.Auth)
	}
	require.Equal(t, map[string][]string{platformAPIBinding: {"controlplane.talgent.svc.cluster.local"}, gatewayBinding: {"connector.talgent.svc.cluster.local"}}, hosts)
	// The Vault reads a binding back normalized; the write is confirmed only
	// when the plan already says what the readback will.
	readback := sdk.CredentialVaultState{Revision: 1, Credentials: []sdk.CredentialMetadata{
		{Name: workIDVaultEntry, SourceType: "inline"}, {Name: workCredentialVaultEntry, SourceType: "inline"}, {Name: spaceTokenVaultEntry, SourceType: "inline"},
	}}
	for _, binding := range plan.Bindings {
		normalized := binding.Match
		normalized.Methods, normalized.Paths = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, []string{"/*"}
		readback.Bindings = append(readback.Bindings, sdk.CredentialBindingMetadata{Name: binding.Name, Match: &normalized, Auth: &sdk.CredentialAuthMetadata{Type: string(sdk.CredentialAuthCustomHeaders)}})
	}
	require.True(t, vaultMatches(&readback, plan, nil), "the Vault's normalized readback confirms the write")

	alone, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api}), "")
	require.NoError(t, err)
	require.Len(t, alone.Credentials, 2, "a Work without an input source has no Space Token")
	require.Len(t, alone.Bindings, 1, "without a Gateway only the native API receives the credential")
	_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("", map[string]string{platformAPIAddressEnv: api}), "")
	require.Error(t, err)
	viaGateway, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: "", gatewayAddressEnv: gateway}), "")
	require.NoError(t, err)
	require.Len(t, viaGateway.Bindings, 1, "a Work reaching the platform through the Gateway alone gives only it the credential")
	require.Equal(t, gatewayBinding, viaGateway.Bindings[0].Name)
	_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", nil), "")
	require.Error(t, err, "every Work reaches the platform")
	// The Vault accepts only fully qualified hosts, so a short Service name or
	// an address is refused here rather than by every credential delivery.
	for _, address := range []string{"http://controlplane", "http://10.43.0.5", "http://controlplane.talgent.svc.cluster.local:8081", "ftp://controlplane.talgent.svc.cluster.local", "http://controlplane.talgent.svc.cluster.local/api"} {
		_, err := withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: address}), "")
		require.Error(t, err, address)
		_, err = withPlatformCredentials(sdk.CredentialVaultCreateRequest{}, sandboxEnvironment("credential-1", map[string]string{platformAPIAddressEnv: api, gatewayAddressEnv: address}), "")
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

func TestWorkWithoutInputSourceMountsNoInputs(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	spec, err := provider.specification(context.Background(), input, testMounts, owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	for _, volume := range spec.Volumes {
		require.NotEqual(t, "/workspace/inputs", volume.MountPath)
	}
}

// A project-scoped definition's Works have no Session Workspace: the sandbox mounts
// only HOME, and its drafts stay in the sandbox's own /workspace, ending
// with it.
func TestWorkWithoutSessionWorkspaceMountsNone(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	input.WorkspaceReference = ""
	mounts := domain.SandboxMounts{Home: testMounts.Home}
	spec, err := provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	require.Len(t, spec.Volumes, 1)
	require.Equal(t, "/home/talgent", spec.Volumes[0].MountPath)
	input.WorkspaceReference = "workspace"
	_, err = provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
	require.ErrorIs(t, err, domain.ErrExecutionRequest, "a Work with a Session Workspace needs its grant")
}

// A missing or mis-scoped grant fails the start instead of being repaired.
func TestSandboxRefusesMissingOrMisScopedGrants(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	for name, change := range map[string]func(*domain.SandboxMounts){
		"no inputs grant":        func(m *domain.SandboxMounts) { m.Inputs = nil },
		"writable inputs":        func(m *domain.SandboxMounts) { m.Inputs.ReadOnly = false },
		"read-only workspace":    func(m *domain.SandboxMounts) { m.Workspace.ReadOnly = true },
		"no home grant":          func(m *domain.SandboxMounts) { m.Home = domain.MountGrant{} },
		"escaping subPath":       func(m *domain.SandboxMounts) { m.Workspace.SubPath = "../other" },
		"absolute subPath":       func(m *domain.SandboxMounts) { m.Home.SubPath = "/homes/key" },
		"unclean inputs subPath": func(m *domain.SandboxMounts) { m.Inputs.SubPath = "talgent/assets/../homes" },
		"whole claim as a mount": func(m *domain.SandboxMounts) { m.Workspace.SubPath = "." },
	} {
		input, mounts := withInputs(request()), inputMounts()
		change(&mounts)
		_, err := provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
		require.ErrorIs(t, err, domain.ErrExecutionRequest, name)
	}
}

// The stored start keeps the input source, so a credential delivery after a
// restart still hands the Space Token to the Vault; a start without one
// stores what it stored before.
func TestStoredStartKeepsItsInputSource(t *testing.T) {
	encoded, err := canonicalRequest(withInputs(request()))
	require.NoError(t, err)
	decoded, err := decodeStartRequest(encoded)
	require.NoError(t, err)
	require.Equal(t, domain.WorkInputSource{Reference: "file-space/case-1", SpaceToken: "space-token"}, decoded.InputSource)
	plain, err := canonicalRequest(request())
	require.NoError(t, err)
	require.NotContains(t, string(plain), "InputSource")
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

// Each read-only mount its caller supplied appears read-only at
// /workspace/<name>, and only those: a grant Storage did not issue read-only,
// or did not issue at all, fails the start.
func TestSandboxPlacesEachReadOnlyMountUnderItsName(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	input.WorkspaceReference = ""
	input.ReadOnlyMounts = []domain.ReadOnlyMount{{Name: "reference", Reference: "project-1"}}
	reference := domain.MountGrant{ClaimName: "juicefs-inputs", SubPath: "talgent/assets/references/project-1", ReadOnly: true}
	mounts := domain.SandboxMounts{Home: testMounts.Home, ReadOnly: map[string]domain.MountGrant{"reference": reference}}
	spec, err := provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	require.Len(t, spec.Volumes, 2)
	mounted := spec.Volumes[1]
	require.Equal(t, "/workspace/reference", mounted.MountPath)
	require.Equal(t, "talgent/assets/references/project-1", mounted.SubPath)
	require.True(t, mounted.ReadOnly, "no Work edits a read-only mount")
	require.Equal(t, input.ReadOnlyMounts, input.MountRequest().ReadOnlyMounts)

	writable := reference
	writable.ReadOnly = false
	mounts.ReadOnly["reference"] = writable
	_, err = provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	delete(mounts.ReadOnly, "reference")
	_, err = provider.specification(context.Background(), input, mounts, owner(input), "test-token", "work-credential-fixture")
	require.ErrorIs(t, err, domain.ErrExecutionRequest)

	input.ReadOnlyMounts = nil
	spec, err = provider.specification(context.Background(), input, domain.SandboxMounts{Home: testMounts.Home}, owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	require.Len(t, spec.Volumes, 1, "a Work without read-only mounts mounts none")
}
