package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// parseProxies parses the value of STORYCREEP_TRUSTED_PROXIES: IP addresses
// and CIDR prefixes, separated by commas, with white space around each. An
// address is the prefix of that address alone, and a prefix is masked, so
// that 10.1.2.3/8 is 10.0.0.0/8. An IPv4 address written as an IPv6 one,
// such as ::ffff:10.0.0.1, is the IPv4 address, as the server sees a
// connection from it.
func parseProxies(v string) ([]netip.Prefix, error) {
	var proxies []netip.Prefix
	for i, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		var p netip.Prefix
		var err error
		if strings.Contains(entry, "/") {
			p, err = netip.ParsePrefix(entry)
		} else {
			var addr netip.Addr
			if addr, err = netip.ParseAddr(entry); err == nil {
				addr = addr.WithZone("")
				p = netip.PrefixFrom(addr, addr.BitLen())
			}
		}
		if err != nil {
			return nil, fmt.Errorf("entry %d is not an IP address or a CIDR prefix, such as 10.0.0.0/8", i+1)
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		proxies = append(proxies, p.Masked())
	}
	return proxies, nil
}

// proxiesString returns proxies as STORYCREEP_TRUSTED_PROXIES writes them,
// with an address alone without its prefix length.
func proxiesString(proxies []netip.Prefix) string {
	s := make([]string, len(proxies))
	for i, p := range proxies {
		s[i] = p.String()
		if p.IsSingleIP() {
			s[i] = p.Addr().String()
		}
	}
	return strings.Join(s, ", ")
}
