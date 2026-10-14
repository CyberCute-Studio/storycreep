package config

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePublicURL(t *testing.T) {
	valid := []struct {
		in, want string
		https    bool
	}{
		{"https://storycreep.example.com", "https://storycreep.example.com", true},
		{"https://storycreep.example.com/", "https://storycreep.example.com", true},
		{"HTTPS://Storycreep.Example.COM", "https://storycreep.example.com", true},
		{"https://storycreep.example.com:443", "https://storycreep.example.com", true},
		{"https://storycreep.example.com:8443", "https://storycreep.example.com:8443", true},
		{"http://localhost:8080", "http://localhost:8080", false},
		{"http://192.168.1.20:80", "http://192.168.1.20", false},
		{"http://192.168.1.20:0080", "http://192.168.1.20", false},
		{"http://[::1]:8080", "http://[::1]:8080", false},
		{"https://[2001:DB8::1]:443/", "https://[2001:db8::1]", true},
		{"http://[2001:db8:0:0::1]:8080", "http://[2001:db8::1]:8080", false},
		{"http://host:", "http://host", false},
		{"https://xn--bcher-kva.example", "https://xn--bcher-kva.example", true},
		{"http://my_nas.local:8080", "http://my_nas.local:8080", false},
		{"https://storycreep.example.com.", "https://storycreep.example.com.", true},
	}
	for _, tt := range valid {
		u, err := parsePublicURL(tt.in)
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if u.String() != tt.want || u.HTTPS() != tt.https {
			t.Errorf("%q = %s (https %v), want %s (https %v)", tt.in, u, u.HTTPS(), tt.want, tt.https)
		}
		if got := u.URL().String(); got != tt.want {
			t.Errorf("%q: URL() = %s, want %s", tt.in, got, tt.want)
		}
	}

	invalid := []struct{ in, want string }{
		{"storycreep.example.com", "must start with http:// or https://"},
		{"localhost:8080", "must start with http:// or https://"},
		{"https:storycreep.example.com", "must start with http:// or https://"},
		{"ftp://storycreep.example.com", "the scheme must be http or https"},
		{"https://user:pass@storycreep.example.com", "must not contain a user name or password"},
		{"https://user@storycreep.example.com", "must not contain a user name or password"},
		{"https://user:pa ss@storycreep.example.com", "not a valid URL"},
		{"ftp://user:pass@storycreep.example.com", "the scheme must be http or https"},
		{"user:pass@storycreep.example.com", "must start with http:// or https://"},
		{"https://storycreep.example.com/@x", "must not have a path"},
		{"https://", "has no host name"},
		{"https://:8443", "has no host name"},
		{"https://example.com/storycreep", "must not have a path"},
		{"https://example.com?x=1", "must not have a query"},
		{"https://example.com/?", "must not have a query"},
		{"https://example.com/#top", "must not have a fragment"},
		{"https://example.com:0", "the port must be between 1 and 65535"},
		{"https://example.com:65536", "the port must be between 1 and 65535"},
		{"https://example.com:http", "not a valid URL"},
		{"https://exa mple.com", "not a valid URL"},
		{"https://example.com\n", "not a valid URL"},
		// Hosts a browser writes differently in the Origin header, or rejects.
		{"https://Bücher.example", "write an internationalized domain name in its xn-- (Punycode) form"},
		{"https://ex<ample.com", "may contain only letters, digits, hyphens, underscores, and dots"},
		{"https://*.example.com", "may contain only letters, digits, hyphens, underscores, and dots"},
		{"https://example..com", "has an empty label"},
		{"https://.example.com", "has an empty label"},
		{"http://0x7f.1", "not a valid IPv4 address"},
		{"http://127.1", "not a valid IPv4 address"},
		{"http://192.168.001.020", "not a valid IPv4 address"},
		{"http://999.1.1.1", "not a valid IPv4 address"},
		{"http://[fe80::1%25en0]:8080", "must not have an IPv6 zone"},
		{"http://[::ffff:192.168.1.20]", "must not be an IPv4-mapped IPv6 address"},
		// The address a server listens on, which no browser elsewhere opens.
		{"http://0.0.0.0:8080", "must not be 0.0.0.0 or ::"},
		{"http://[::]:8080", "must not be 0.0.0.0 or ::"},
		{"https://user@Bücher.example", "must not contain a user name or password"},
	}
	for _, tt := range invalid {
		u, err := parsePublicURL(tt.in)
		if err == nil {
			t.Errorf("%q parsed as %s, want an error", tt.in, u)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: error %q, want %q", tt.in, err, tt.want)
		}
		// Only a value that may hold a password is hidden.
		var hidden hiddenValueError
		if got, want := errors.As(err, &hidden), strings.Contains(tt.in, "@"); got != want {
			t.Errorf("%q: hidden = %v, want %v", tt.in, got, want)
		}
	}
}

func TestPublicURLCopy(t *testing.T) {
	u, err := parsePublicURL("https://storycreep.example.com")
	if err != nil {
		t.Fatal(err)
	}
	u.URL().Host = "evil.example.com"
	if u.String() != "https://storycreep.example.com" {
		t.Errorf("changing the copy changed the URL to %s", u)
	}
}
