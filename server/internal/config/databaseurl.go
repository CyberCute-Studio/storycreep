package config

import (
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// DatabaseURL is a PostgreSQL connection URL, such as
// postgres://storycreep:password@db:5432/storycreep (STORYCREEP_DATABASE_URL
// and STORYCREEP_DATABASE_ADMIN_URL). The URL carries a password, so it is
// held as a secret: String shows it with the password redacted, and only
// Reveal returns it.
//
// The zero DatabaseURL is unset.
type DatabaseURL struct {
	url      secret.Value
	display  string
	user     string
	database string
	password secret.Value
}

// reservedDatabaseParams are the query parameters that would set what the
// URL's user, password, and path already say. They are rejected, so that the
// login the server provisions and checks is the one it connects as.
var reservedDatabaseParams = []string{"user", "password", "dbname", "database"}

// secretDatabaseParams are the query parameters whose values are redacted
// when the URL is shown.
var secretDatabaseParams = []string{"sslpassword"}

// ParseDatabaseURL parses a PostgreSQL connection URL with a user and a
// database. Its errors never quote the URL, which may hold a password.
func ParseDatabaseURL(v string) (DatabaseURL, error) {
	fail := func(msg string) (DatabaseURL, error) {
		return DatabaseURL{}, hiddenValueError{errors.New(msg)}
	}
	// pgx reads a value as a URL only if it starts with one of these, in
	// lowercase, and reads anything else as keyword=value settings, which
	// would ignore the URL. url.Parse accepts the scheme in any case.
	if !strings.HasPrefix(v, "postgres://") && !strings.HasPrefix(v, "postgresql://") {
		return fail("must start with postgres:// or postgresql://, in lowercase")
	}
	// pgx reads a URL as libpq does, which ends the user and password at the
	// first @, where url.Parse ends them at the last one. With a second @,
	// the server would log in with the start of the password, at a host
	// made of the rest, and the connection error would show that rest.
	_, rest, _ := strings.Cut(v, "://")
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if strings.Count(rest, "@") > 1 {
		return fail("has an @ in its user or password that is not percent-encoded; write it as %40")
	}
	// The parser's explanation can quote part of a password.
	u, err := url.Parse(v)
	if err != nil {
		return fail("not a valid URL")
	}
	switch {
	case u.User == nil || u.User.Username() == "":
		return fail("names no user; write it in front of the host, as in postgres://storycreep:password@db/storycreep")
	case u.Path == "" || u.Path == "/":
		return fail("names no database; write it as the path, as in postgres://storycreep:password@db/storycreep")
	case strings.Contains(strings.TrimPrefix(u.Path, "/"), "/"):
		return fail("the path must be only the database name")
	case u.Fragment != "":
		return fail("must not have a fragment")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fail("the query is not valid")
	}
	// libpq, and pgx with it, also needs exactly one = in each parameter,
	// which url.ParseQuery does not.
	for q := u.RawQuery; q != ""; {
		var pair string
		pair, q, _ = strings.Cut(q, "&")
		if strings.Count(pair, "=") != 1 {
			return fail("each query parameter must be name=value, with an = in the value percent-encoded as %3D")
		}
	}
	for _, p := range reservedDatabaseParams {
		if query.Has(p) {
			return fail("must not set " + p + " as a query parameter; write the user, password, and database " +
				"as in postgres://storycreep:password@db/storycreep")
		}
	}

	d := DatabaseURL{
		url:      secret.New(v),
		user:     u.User.Username(),
		database: strings.TrimPrefix(u.Path, "/"),
	}
	userinfo := url.User(d.user).String()
	if pw, ok := u.User.Password(); ok && pw != "" {
		d.password = secret.New(pw)
		userinfo += ":" + secret.Redacted
	}
	d.display = u.Scheme + "://" + userinfo + "@" + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		d.display += "?" + redactQuery(u.RawQuery)
	}
	return d, nil
}

// redactQuery returns the query q, in its original order, with the values of
// secretDatabaseParams redacted.
func redactQuery(q string) string {
	pairs := strings.Split(q, "&")
	for i, pair := range pairs {
		name, _, _ := strings.Cut(pair, "=")
		if name, err := url.QueryUnescape(name); err == nil && slices.Contains(secretDatabaseParams, name) {
			pairs[i] = url.QueryEscape(name) + "=" + secret.Redacted
		}
	}
	return strings.Join(pairs, "&")
}

// IsSet reports whether the URL is set.
func (d DatabaseURL) IsSet() bool {
	return d.url.IsSet()
}

// Reveal returns the URL with its password, for connecting. Call it only
// where the connection is made, never to log or display it.
func (d DatabaseURL) Reveal() string {
	return d.url.Reveal()
}

// User returns the login the URL connects as.
func (d DatabaseURL) User() string {
	return d.user
}

// Database returns the name of the database the URL connects to.
func (d DatabaseURL) Database() string {
	return d.database
}

// Password returns the URL's password, which is unset if the URL has none.
func (d DatabaseURL) Password() secret.Value {
	return d.password
}

// String returns the URL with its password, and the values of query
// parameters that hold secrets, replaced by secret.Redacted.
func (d DatabaseURL) String() string {
	return d.display
}

// GoString returns the same as String, for the %#v verb.
func (d DatabaseURL) GoString() string {
	return d.display
}

// LogValue returns the same as String, for slog.
func (d DatabaseURL) LogValue() slog.Value {
	return slog.StringValue(d.display)
}

// MarshalText returns the same as String.
func (d DatabaseURL) MarshalText() ([]byte, error) {
	return []byte(d.display), nil
}
