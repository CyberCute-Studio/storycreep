package config

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PublicURL is the URL users open the instance at (STORYCREEP_PUBLIC_URL). It
// is an origin, written as a browser writes it in an Origin header: a scheme,
// a lowercase host, and a port only when it is not the scheme's default, such
// as https://storycreep.example.com.
type PublicURL struct {
	scheme string
	// host is the host name, with the port if there is one.
	host string
}

// String returns the URL in origin form, without a trailing slash.
func (u PublicURL) String() string {
	return u.scheme + "://" + u.host
}

// HTTPS reports whether the URL is https.
func (u PublicURL) HTTPS() bool {
	return u.scheme == "https"
}

// URL returns a copy of the URL, for building links.
func (u PublicURL) URL() *url.URL {
	return &url.URL{Scheme: u.scheme, Host: u.host}
}

// parsePublicURL parses v. A value with an @ is never valid, and text in front
// of the @ may be a password, so every error for such a value quotes none of
// it and is a hiddenValueError, which Load does not quote v after either.
func parsePublicURL(v string) (PublicURL, error) {
	hide := strings.Contains(v, "@")
	fail := func(msg string) (PublicURL, error) {
		if hide {
			return PublicURL{}, hiddenValueError{errors.New(msg)}
		}
		return PublicURL{}, errors.New(msg)
	}

	u, err := url.Parse(v)
	if err != nil {
		if hide {
			// The parser's explanation can quote part of a password.
			return fail("not a valid URL")
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fail("not a valid URL: " + err.Error())
	}
	switch {
	case u.Scheme == "" || u.Opaque != "":
		return fail("must start with http:// or https://")
	case u.Scheme != "http" && u.Scheme != "https":
		return fail("the scheme must be http or https")
	case u.User != nil:
		return fail("must not contain a user name or password")
	case u.Hostname() == "":
		return fail("has no host name")
	case u.Path != "" && u.Path != "/":
		return fail("must not have a path; Storycreep is served at the root of its host name")
	case u.RawQuery != "" || u.ForceQuery:
		return fail("must not have a query")
	case u.Fragment != "":
		return fail("must not have a fragment")
	}

	host, msg := canonicalHost(u.Hostname())
	if msg != "" {
		return fail(msg)
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fail("the port must be between 1 and 65535")
		}
		port = strconv.Itoa(n)
		if u.Scheme == "http" && n == 80 || u.Scheme == "https" && n == 443 {
			port = ""
		}
	}
	switch {
	case port != "":
		host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		host = "[" + host + "]"
	}
	return PublicURL{scheme: u.Scheme, host: host}, nil
}

// canonicalHost returns host, as url.URL.Hostname gives it, in the form a
// browser writes in an Origin header, with an IPv6 address unbracketed. If a
// browser would write the host differently, or reject it, canonicalHost
// explains why instead, so that the public URL always compares equal to the
// Origin of a request made to it.
func canonicalHost(host string) (string, string) {
	host = strings.ToLower(host)
	if addr, err := netip.ParseAddr(host); err == nil {
		switch {
		case addr.Zone() != "":
			return "", "must not have an IPv6 zone"
		case addr.Is4In6():
			return "", "must not be an IPv4-mapped IPv6 address; use the IPv4 address"
		case addr.IsUnspecified():
			return "", "must not be 0.0.0.0 or ::, which a server listens on but browsers cannot open; " +
				"write the host name or IP address users open"
		}
		return addr.String(), ""
	}
	if strings.Contains(host, ":") {
		return "", "has an invalid IPv6 address"
	}
	for _, r := range host {
		switch {
		case r >= utf8.RuneSelf:
			return "", "the host name must be ASCII; write an internationalized domain name in its xn-- (Punycode) form, which is what browsers send"
		case !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'):
			return "", "the host name may contain only letters, digits, hyphens, underscores, and dots"
		}
	}
	// A trailing dot is allowed: browsers keep it in the Origin header.
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if slices.Contains(labels, "") {
		return "", "the host name has an empty label"
	}
	// Browsers read a host name whose last label is a number as an IPv4
	// address in shorthand, octal, or hex, such as 0x7f.1 for 127.0.0.1.
	if isIPv4Number(labels[len(labels)-1]) {
		return "", "not a valid IPv4 address; write it as four decimal numbers, such as 192.168.1.20"
	}
	return host, ""
}

// isIPv4Number reports whether the URL Standard reads label, which is not
// empty, as a number of an IPv4 address: decimal digits, or hex digits after
// 0x.
func isIPv4Number(label string) bool {
	if rest, ok := strings.CutPrefix(label, "0x"); ok {
		return strings.Trim(rest, "0123456789abcdef") == ""
	}
	return strings.Trim(label, "0123456789") == ""
}
