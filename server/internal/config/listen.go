package config

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// errListenForm is checkListenAddr's explanation of a value that is not
// written host:port or :port at all.
var errListenForm = errors.New("must be host:port, or :port for every network interface, with an IPv6 address in " +
	"brackets")

// checkListenAddr explains why v is not an address to listen on, as
// STORYCREEP_HTTP_ADDR and STORYCREEP_ADMIN_ADDR are written: host:port or
// :port, with an IP address or a host name and a port number. The port must
// be a number, although net.Listen also reads service names such as "http",
// because a name means a different port on another system. A value that is
// not host:port or :port at all is explained by errListenForm.
func checkListenAddr(v string) error {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return errListenForm
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("the port must be a number between 1 and 65535")
	}
	if host == "" {
		return nil
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if !isHostName(host) {
		return errors.New("the host must be an IP address, such as 127.0.0.1, or a host name: labels of letters, " +
			"digits, and hyphens, separated by dots, none of them empty or starting or ending with a hyphen, and " +
			"the last not a number")
	}
	return nil
}

// isHostName reports whether host is a host name (RFC 1123): labels of 1 to
// 63 letters, digits, and hyphens, which neither start nor end with a
// hyphen, separated by dots, with an optional dot at the end. Its last label
// must not be a number, decimal or 0x hexadecimal, as in the WHATWG URL
// standard, because resolvers read such a name, as "0" or "127.1", as an
// IPv4 address written short, which listenersOverlap could not compare with
// the other listener's address.
func isHostName(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		if strings.Trim(strings.ToLower(l), "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return false
		}
	}
	last := strings.ToLower(labels[len(labels)-1])
	if strings.Trim(last, "0123456789") == "" {
		return false
	}
	if hex, ok := strings.CutPrefix(last, "0x"); ok && strings.Trim(hex, "0123456789abcdef") == "" {
		return false
	}
	return true
}

// listenersOverlap reports whether listening on a and on b, which
// checkListenAddr accepts, takes the same port on a network interface they
// share: their ports are the same, and so are their hosts, or one of them is
// every interface (no host, 0.0.0.0, or ::). Linux refuses the second
// listener then, but Windows and macOS let both listen, and give the
// connections to the more specific address to its listener, so that the two
// would quietly split the port's traffic.
func listenersOverlap(a, b string) bool {
	hostA, portA, errA := net.SplitHostPort(a)
	hostB, portB, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil || portA != portB {
		return false
	}
	if everyInterface(hostA) || everyInterface(hostB) {
		return true
	}
	ipA, errA := netip.ParseAddr(hostA)
	ipB, errB := netip.ParseAddr(hostB)
	if errA == nil && errB == nil {
		return ipA == ipB
	}
	return strings.EqualFold(hostA, hostB)
}

// everyInterface reports whether host, as a listen address writes it, means
// every network interface.
func everyInterface(host string) bool {
	if host == "" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsUnspecified()
}

// minTokenLength is the shortest bearer token accepted, so that a placeholder
// such as "changeme" is not taken for one.
const minTokenLength = 16

// checkBearerToken explains why v is not a bearer token of RFC 6750 §2.1
// with at least minTokenLength letters, digits, and -._~+/, followed by any
// number of '=', which do not count towards the length. The explanation
// never contains v, so it is a hiddenValueError.
func checkBearerToken(v string) error {
	body := strings.TrimRight(v, "=")
	for _, r := range body {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~+/", r)
		if !ok {
			return hiddenValueError{errors.New("may contain only letters, digits, and -._~+/, followed by any number of =")}
		}
	}
	switch {
	case body == "":
		return hiddenValueError{errors.New("must start with a letter, a digit, or one of -._~+/")}
	case len(body) < minTokenLength:
		return hiddenValueError{errors.New("must be at least " + strconv.Itoa(minTokenLength) +
			" characters long, not counting the = at its end")}
	}
	return nil
}
