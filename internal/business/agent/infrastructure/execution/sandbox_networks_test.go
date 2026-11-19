package execution

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// deniedBy builds the egress policy of a sandbox under the declarations and
// reports whether it denies an address.
func deniedBy(t *testing.T, enterprise, cluster string) func(string) bool {
	t.Helper()
	networks, err := ParseSandboxNetworks(enterprise, cluster)
	require.NoError(t, err)
	sandboxes := &Sandboxes{config: SandboxConfig{Environment: map[string]string{platformAPIAddressEnv: fixturePlatformAPIAddress}, Networks: networks, NetworkAllow: []string{"10.43.0.10"}}}
	policy, err := sandboxes.networkPolicy(context.Background())
	require.NoError(t, err)
	return func(raw string) bool {
		ip := netip.MustParseAddr(raw)
		for _, rule := range policy.Egress {
			if prefix, err := netip.ParsePrefix(rule.Target); err == nil && rule.Action == "deny" && prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
}

// inside is an address of the network, past its first; the tests name
// networks rather than individual private addresses.
func inside(network string) string {
	return netip.MustParsePrefix(network).Addr().Next().Next().String()
}

// Without a declared network every private range stays denied.
func TestWithoutEnterpriseNetworksPrivateRangesStayDenied(t *testing.T) {
	denied := deniedBy(t, "", "10.42.0.0/16 10.43.0.0/16")
	for _, network := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "10.42.0.0/16"} {
		require.True(t, denied(inside(network)), network)
	}
	require.True(t, denied("10.43.0.24"), "another Service stays unreachable")
	require.False(t, denied("8.8.8.8"), "the internet stays reachable")
	require.False(t, denied("10.43.0.10"), "cluster DNS stays reachable")
}

// A declared network opens to the sandbox; the other private ranges stay
// denied.
func TestADeclaredEnterpriseNetworkIsReachable(t *testing.T) {
	denied := deniedBy(t, "172.16.0.0/12:443,8443 100.64.0.0/10:80", "10.42.0.0/16 10.43.0.0/16")
	require.False(t, denied(inside("172.16.0.0/12")))
	require.False(t, denied(inside("100.64.0.0/10")))
	require.True(t, denied(inside("10.0.0.0/8")), "an undeclared private range stays denied")
	require.True(t, denied(inside("192.168.0.0/16")), "an undeclared private range stays denied")
	require.True(t, denied(inside("169.254.0.0/16")), "metadata stays denied")
}

// A declared network that contains cluster ranges leaves them denied.
func TestAClusterRangeInsideADeclaredNetworkStaysDenied(t *testing.T) {
	denied := deniedBy(t, "10.0.0.0/8:443", "10.42.0.0/16 10.43.0.0/16")
	require.False(t, denied(inside("10.0.0.0/8")))
	require.True(t, denied(inside("10.42.0.0/16")), "the pod network stays denied")
	require.True(t, denied("10.43.0.24"), "the Service network stays denied")
	require.False(t, denied("10.43.0.10"), "cluster DNS stays reachable")
}

// The declarations refuse what the NetworkPolicy could not mirror or what
// would reach nothing new.
func TestSandboxNetworksRefuseInvalidDeclarations(t *testing.T) {
	for name, declaration := range map[string][2]string{
		"no ports":               {"172.16.0.0/12", ""},
		"empty port":             {"172.16.0.0/12:", ""},
		"port zero":              {"172.16.0.0/12:0", ""},
		"port too large":         {"172.16.0.0/12:65536", ""},
		"host bits set":          {"10.43.0.10/8:443", ""},
		"IPv6":                   {"fd00::/8:443", ""},
		"public network":         {"8.8.8.0/24:443", ""},
		"wider than private":     {"8.0.0.0/7:443", ""},
		"inside a cluster range": {"10.42.0.0/24:443", "10.42.0.0/16"},
		"cluster range invalid":  {"", "10.42.0.0"},
		"no cluster ranges":      {"172.16.0.0/12:443", ""},
		"leading zero":           {"010.0.0.0/8:443", "10.42.0.0/16"},
	} {
		_, err := ParseSandboxNetworks(declaration[0], declaration[1])
		require.Error(t, err, name)
	}
	networks, err := ParseSandboxNetworks(" 172.16.0.0/12:443,8443\n100.64.0.0/10:80 ", "10.42.0.0/16")
	require.NoError(t, err)
	require.Equal(t, []EnterpriseNetwork{
		{Prefix: netip.MustParsePrefix("172.16.0.0/12"), Ports: []uint16{443, 8443}},
		{Prefix: netip.MustParsePrefix("100.64.0.0/10"), Ports: []uint16{80}},
	}, networks.Enterprise)
}
