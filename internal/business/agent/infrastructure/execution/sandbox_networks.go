package execution

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// privateRanges are the private IPv4 ranges a sandbox reaches only where the
// deployment declares an enterprise network inside them.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
}

// reservedRanges stay denied whatever the deployment declares: the metadata
// link-local range, and addresses that are no unicast destination. All of
// IPv6 is denied too.
var reservedRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/0"),
}

// EnterpriseNetwork is a private network the deployment declares reachable
// from Cloud Works, with the TCP ports its services listen on. The sandbox's
// egress sidecar opens the whole network: under its default-allow policy a
// deny rule's ports name what it denies, and it rejects deny before any
// allow, so it cannot open only some ports. The sandbox NetworkPolicy,
// rendered from the same declaration, limits the network to its ports.
type EnterpriseNetwork struct {
	Prefix netip.Prefix
	Ports  []uint16
}

// SandboxNetworks are the private networks the deployment declares: the
// enterprise networks a sandbox reaches, and the cluster's own ranges (pod
// and Service networks, nodes, storage backends), which stay denied even
// where an enterprise network contains them.
type SandboxNetworks struct {
	Enterprise []EnterpriseNetwork
	Cluster    []netip.Prefix
}

// ParseSandboxNetworks reads the declarations as the deployment's environment
// holds them, ENTERPRISE_NETWORKS and CLUSTER_NETWORKS:
// whitespace-separated "CIDR:PORT[,PORT...]" entries and whitespace-separated
// CIDRs. scripts/deploy/lib/networks.sh reads the same values into the
// sandbox NetworkPolicy and refuses what this refuses.
func ParseSandboxNetworks(enterprise, cluster string) (SandboxNetworks, error) {
	networks := SandboxNetworks{}
	for _, entry := range strings.Fields(enterprise) {
		rawPrefix, rawPorts, found := strings.Cut(entry, ":")
		if !found {
			return SandboxNetworks{}, fmt.Errorf("agent: enterprise network %q needs its ports, as CIDR:PORT[,PORT...]", entry)
		}
		prefix, err := parseNetwork(rawPrefix)
		if err != nil {
			return SandboxNetworks{}, err
		}
		network := EnterpriseNetwork{Prefix: prefix}
		for _, rawPort := range strings.Split(rawPorts, ",") {
			port, err := strconv.ParseUint(rawPort, 10, 16)
			if err != nil || port == 0 {
				return SandboxNetworks{}, fmt.Errorf("agent: enterprise network %q has an invalid port %q", entry, rawPort)
			}
			network.Ports = append(network.Ports, uint16(port))
		}
		networks.Enterprise = append(networks.Enterprise, network)
	}
	for _, raw := range strings.Fields(cluster) {
		prefix, err := parseNetwork(raw)
		if err != nil {
			return SandboxNetworks{}, err
		}
		networks.Cluster = append(networks.Cluster, prefix)
	}
	return networks, networks.Validate()
}

// parseNetwork takes an IPv4 CIDR written with its host bits clear, so the
// configuration and the NetworkPolicy name the same network.
func parseNetwork(raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
		return netip.Prefix{}, fmt.Errorf("agent: %q is not an IPv4 network in CIDR notation with its host bits clear", raw)
	}
	return prefix, nil
}

// Validate refuses an enterprise network outside the private ranges, where a
// sandbox already reaches every address, and one inside a cluster range,
// which stays unreachable.
func (n SandboxNetworks) Validate() error {
	// Without its cluster ranges, a declared network could contain the
	// cluster's own addresses unnoticed.
	if len(n.Enterprise) > 0 && len(n.Cluster) == 0 {
		return errors.New("agent: declaring enterprise networks needs the cluster's own ranges")
	}
	for _, network := range n.Enterprise {
		if len(network.Ports) == 0 {
			return fmt.Errorf("agent: enterprise network %s declares no port", network.Prefix)
		}
		if !slices.ContainsFunc(privateRanges, func(p netip.Prefix) bool { return contains(p, network.Prefix) }) {
			return fmt.Errorf("agent: enterprise network %s lies outside the private ranges", network.Prefix)
		}
		for _, cluster := range n.Cluster {
			if contains(cluster, network.Prefix) {
				return fmt.Errorf("agent: enterprise network %s lies within the cluster's own range %s", network.Prefix, cluster)
			}
		}
	}
	for _, cluster := range n.Cluster {
		if !cluster.IsValid() || !cluster.Addr().Is4() {
			return fmt.Errorf("agent: cluster range %s is not an IPv4 network", cluster)
		}
	}
	return nil
}

func (n SandboxNetworks) clone() SandboxNetworks {
	enterprise := make([]EnterpriseNetwork, 0, len(n.Enterprise))
	for _, network := range n.Enterprise {
		enterprise = append(enterprise, EnterpriseNetwork{Prefix: network.Prefix, Ports: slices.Clone(network.Ports)})
	}
	return SandboxNetworks{Enterprise: enterprise, Cluster: slices.Clone(n.Cluster)}
}

// contains reports whether outer holds every address of inner.
func contains(outer, inner netip.Prefix) bool {
	return outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}

// excludePrefix splits prefix into the largest prefixes that hold none of
// cut's addresses.
func excludePrefix(prefix, cut netip.Prefix) []netip.Prefix {
	if !prefix.Overlaps(cut) {
		return []netip.Prefix{prefix}
	}
	if contains(cut, prefix) {
		return nil
	}
	bits := prefix.Bits() + 1
	first := netip.PrefixFrom(prefix.Addr(), bits)
	bytes := prefix.Addr().As16()
	offset := 0
	if prefix.Addr().Is4() {
		offset = 12
	}
	bit := prefix.Bits()
	bytes[offset+bit/8] |= 1 << uint(7-bit%8)
	addr := netip.AddrFrom16(bytes)
	if prefix.Addr().Is4() {
		addr = addr.Unmap()
	}
	second := netip.PrefixFrom(addr, bits)
	return append(excludePrefix(first, cut), excludePrefix(second, cut)...)
}
