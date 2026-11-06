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
	result := &sdk.NetworkPolicy{DefaultAction: "allow"}
	targets := append([]string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "0.0.0.0/8", "224.0.0.0/4", "240.0.0.0/4", "::/0"}, s.config.NetworkDeny...)
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
				next = append(next, excludeAddress(p, ip)...)
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
	result.Egress = slices.Compact(result.Egress)
	return result, nil
}

func excludeAddress(prefix netip.Prefix, ip netip.Addr) []netip.Prefix {
	if !prefix.Contains(ip) {
		return []netip.Prefix{prefix}
	}
	if prefix.Bits() == ip.BitLen() {
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
	return append(excludeAddress(first, ip), excludeAddress(second, ip)...)
}
