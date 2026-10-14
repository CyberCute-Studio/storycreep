package config

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// PublicURL is the URL users open the instance at (STORYCREEP_PUBLIC_URL). It
// is an origin: a scheme, a lowercase host, and a port only when it is not the
// scheme's default, such as https://storycreep.example.com.
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

func parsePublicURL(v string) (PublicURL, error) {
	u, err := url.Parse(v)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return PublicURL{}, errors.New("not a valid URL: " + err.Error())
	}
	switch {
	case u.Scheme == "" || u.Opaque != "":
		return PublicURL{}, errors.New("must start with http:// or https://")
	case u.Scheme != "http" && u.Scheme != "https":
		return PublicURL{}, errors.New("the scheme must be http or https")
	case u.User != nil:
		return PublicURL{}, errors.New("must not contain a user name or password")
	case u.Hostname() == "":
		return PublicURL{}, errors.New("has no host name")
	case u.Path != "" && u.Path != "/":
		return PublicURL{}, errors.New("must not have a path; Storycreep is served at the root of its host name")
	case u.RawQuery != "" || u.ForceQuery:
		return PublicURL{}, errors.New("must not have a query")
	case u.Fragment != "":
		return PublicURL{}, errors.New("must not have a fragment")
	}

	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return PublicURL{}, errors.New("the port must be between 1 and 65535")
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
