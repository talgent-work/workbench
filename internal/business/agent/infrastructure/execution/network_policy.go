package execution

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"slices"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

// lookupHost resolves a platform endpoint the sandbox may reach.
var lookupHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// networkPolicy subtracts necessary service addresses before emitting nft deny
// rules: native nft rejects deny before allow, including a more-specific allow.
// CNI independently limits the resulting address exceptions to approved ports.
func (s *Sandboxes) networkPolicy(ctx context.Context) (*sdk.NetworkPolicy, error) {
	allowed := []netip.Addr{}
	for _, raw := range s.config.NetworkAllow {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, errors.New("agent: network exception must be an IP address")
		}
		allowed = append(allowed, ip.Unmap())
	}
	// A sandbox reaches the platform addresses it is given; telemetry goes to
	// the collector.
	addresses, err := platformAddresses(s.config.Environment)
	if err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(addresses)+1)
	for _, address := range addresses {
		hosts = append(hosts, address.origin.Hostname())
	}
	if raw := s.config.Environment["OTEL_EXPORTER_OTLP_ENDPOINT"]; raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return nil, errors.New("agent: invalid native network endpoint")
		}
		hosts = append(hosts, u.Hostname())
	}
	for _, host := range hosts {
		ips, err := lookupHost(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("agent: native network endpoint resolution failed")
		}
		for _, ip := range ips {
			allowed = append(allowed, ip.Unmap())
		}
	}
	// The private ranges open only where an enterprise network is declared;
	// the reserved ranges, the cluster's own ranges and further configured
	// targets stay denied even where an enterprise network contains them,
	// since nft rejects deny before allow.
	private := slices.Clone(privateRanges)
	for _, network := range s.config.Networks.Enterprise {
		next := []netip.Prefix{}
		for _, p := range private {
			next = append(next, excludePrefix(p, network.Prefix)...)
		}
		private = next
	}
	targets := []string{}
	for _, p := range slices.Concat(private, reservedRanges, s.config.Networks.Cluster) {
		targets = append(targets, p.String())
	}
	targets = append(targets, s.config.NetworkDeny...)
	result := &sdk.NetworkPolicy{DefaultAction: "allow"}
	for _, target := range targets {
		prefix, err := netip.ParsePrefix(target)
		if err != nil {
			result.Egress = append(result.Egress, sdk.NetworkRule{Action: "deny", Target: target})
			continue
		}
		prefixes := []netip.Prefix{prefix.Masked()}
		for _, ip := range allowed {
			next := []netip.Prefix{}
			for _, p := range prefixes {
				next = append(next, excludePrefix(p, netip.PrefixFrom(ip, ip.BitLen()))...)
			}
			prefixes = next
		}
		for _, p := range prefixes {
			result.Egress = append(result.Egress, sdk.NetworkRule{Action: "deny", Target: p.String()})
		}
	}
	slices.SortFunc(result.Egress, func(a, b sdk.NetworkRule) int {
		if a.Target < b.Target {
			return -1
		}
		if a.Target > b.Target {
			return 1
		}
		return 0
	})
	result.Egress = slices.CompactFunc(result.Egress, func(a, b sdk.NetworkRule) bool {
		return a.Action == b.Action && a.Target == b.Target && slices.Equal(a.Ports, b.Ports)
	})
	return result, nil
}
