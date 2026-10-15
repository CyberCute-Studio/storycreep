package config

import (
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
	// pool is what pgx read from the URL, which the server connects with.
	pool *pgxpool.Config
}

// reservedDatabaseParams are the query parameters that would set what the
// URL's user, password, and path already say. They are rejected, so that the
// login the server provisions and checks is the one it connects as.
var reservedDatabaseParams = []string{"user", "password", "dbname", "database"}

// shownDatabaseParams are the query parameters whose values are shown when
// the URL is shown: the libpq and pgx settings that hold no secret, and the
// common PostgreSQL settings. Every other parameter's value is redacted, such
// as sslpassword's, and also a name that is misspelt or that libpq reads
// differently, so that a value is shown only when it is known not to be a
// secret.
var shownDatabaseParams = []string{
	"host", "port", "passfile", "connect_timeout", "sslmode", "sslkey", "sslcert", "sslrootcert", "sslnegotiation",
	"sslsni", "krbspn", "krbsrvname", "target_session_attrs", "service", "servicefile", "min_protocol_version",
	"max_protocol_version", "channel_binding", "require_auth",
	"pool_max_conns", "pool_min_conns", "pool_min_idle_conns", "pool_max_conn_lifetime", "pool_max_conn_idle_time",
	"pool_health_check_period", "pool_max_conn_lifetime_jitter", "pool_ping_timeout",
	"statement_cache_capacity", "description_cache_capacity", "default_query_exec_mode",
	"application_name", "search_path", "timezone", "client_encoding",
}

// ParseDatabaseURL parses a PostgreSQL connection URL with a user and a
// database, and checks that pgx, which the server connects with, can use it.
// Its errors never quote the URL, which may hold a password.
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
	// libpq, and pgx with it, skips the spaces around each part of the URL,
	// such as the name of a query parameter, and refuses one inside it. A
	// name written with a space, such as " password", would otherwise pass
	// the checks below as another name, with its value shown unredacted.
	// Control characters are refused too, also in the host, which url.Parse
	// does not see below.
	if strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return fail("has a space or a control character; write a space as %20")
	}
	// libpq and pgx refuse a NUL, and pgx's explanation quotes the part of the
	// URL that holds it, which may be a value that is shown redacted.
	if strings.Contains(v, "%00") {
		return fail("has %00, a NUL character, which no part of the URL may hold")
	}
	// pgx reads a URL as libpq does, which ends the user and password at the
	// first @ or /, so a ? or # before that @ is part of them, where
	// url.Parse would end them there and fail to read the rest.
	scheme, rest, _ := strings.Cut(v, "://")
	if i := strings.IndexAny(rest, "@/"); i >= 0 && rest[i] == '@' && strings.ContainsAny(rest[:i], "?#") {
		return fail("has a ? or # in its user or password that is not percent-encoded; write them as %3F and %23")
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	// Where url.Parse ends the user and password at the last @, libpq ends
	// them at the first. With a second @, the server would log in with the
	// start of the password, at a host made of the rest, and the connection
	// error would show that rest.
	if strings.Count(authority, "@") > 1 {
		return fail("has an @ in its user or password that is not percent-encoded; write it as %40")
	}
	userinfo, host, ok := strings.Cut(authority, "@")
	switch {
	// libpq ends the user and password at the first /, so a / in the
	// password leaves the URL without a user, and with the @ after the /.
	case !ok && strings.Contains(tail, "@"):
		return fail("names no user, or has a / in its user or password that is not percent-encoded; " +
			"write a / there as %2F")
	case !ok:
		return fail("names no user; write it in front of the host, as in postgres://storycreep:password@db/storycreep")
	}
	// url.Parse refuses some hosts that libpq reads, such as a Unix socket
	// directory written as %2Fvar%2Frun%2Fpostgresql, so it reads the URL
	// with a stand-in host, and pgx reads the real one below. The parser's
	// explanation can quote part of a password. It refuses the characters
	// that a URL's user and password cannot hold unencoded, such as a [ or a
	// letter that is not ASCII, which libpq would read, and a % in the path
	// that starts no valid escape.
	u, err := url.Parse(scheme + "://" + userinfo + "@host" + tail)
	if err != nil {
		return fail("not a valid URL; in the user, password, and database name, percent-encode every character " +
			"that is not an ASCII letter, a digit, or one of - . _ ~, as %5B for [")
	}
	switch {
	case u.User == nil || u.User.Username() == "":
		return fail("names no user; write it in front of the host, as in postgres://storycreep:password@db/storycreep")
	case u.Path == "" || u.Path == "/":
		return fail("names no database; write it as the path, as in postgres://storycreep:password@db/storycreep")
	// The path as written, because libpq reads a %2F in it as part of the
	// database name.
	case strings.Contains(strings.TrimPrefix(u.EscapedPath(), "/"), "/"):
		return fail("the path must be only the database name")
	// url.Parse reports an empty fragment as none, but libpq reads any # as
	// part of the database name or of a value.
	case strings.Contains(tail, "#"):
		return fail("must not have a fragment, which starts at a #; write a # in a value as %23")
	}
	// The query is read as libpq reads it: each parameter is name=value, with
	// exactly one =, and both are percent-decoded. url.ParseQuery would also
	// refuse a ; and read a + as a space, which libpq does not.
	var names []string
	for q := u.RawQuery; q != ""; {
		var pair string
		pair, q, _ = strings.Cut(q, "&")
		if strings.Count(pair, "=") != 1 {
			return fail("each query parameter must be name=value, with an = in the value percent-encoded as %3D")
		}
		name, value, _ := strings.Cut(pair, "=")
		name, nameErr := url.PathUnescape(name)
		if _, valueErr := url.PathUnescape(value); nameErr != nil || valueErr != nil {
			return fail("the query is not valid")
		}
		names = append(names, name)
	}
	for _, p := range reservedDatabaseParams {
		if slices.Contains(names, p) {
			return fail("must not set " + p + " as a query parameter; write the user, password, and database " +
				"as in postgres://storycreep:password@db/storycreep")
		}
	}
	// The server connects with what pgx reads from the URL, so a value that
	// pgx refuses, such as sslmode=required or a certificate file that
	// cannot be read, is invalid here too, rather than when the server
	// connects. pgx also reads the libpq environment variables and the files
	// the URL names, as it does then.
	cfg, err := pgxpool.ParseConfig(v)
	if err != nil {
		return fail(pgxReason(err))
	}

	d := DatabaseURL{
		url:      secret.New(v),
		user:     cfg.ConnConfig.User,
		database: cfg.ConnConfig.Database,
		pool:     cfg,
	}
	shownUser := url.User(d.user).String()
	if pw, ok := u.User.Password(); ok && pw != "" {
		d.password = secret.New(pw)
		shownUser += ":" + secret.Redacted
	}
	d.display = scheme + "://" + shownUser + "@" + host + u.EscapedPath()
	if u.RawQuery != "" {
		d.display += "?" + redactQuery(u.RawQuery)
	}
	return d, nil
}

// pgxReason returns why pgx refused a URL, without the URL, which pgx's
// errors quote with only the passwords it recognizes masked.
func pgxReason(err error) string {
	var perr *pgconn.ParseConfigError
	if !errors.As(err, &perr) {
		return "the server's PostgreSQL client cannot read it"
	}
	c := *perr
	c.ConnString = ""
	return strings.TrimPrefix(c.Error(), "cannot parse ``: ")
}

// redactQuery returns the query q, in its original order, with the value of
// every parameter that is not one of shownDatabaseParams redacted. The names
// are decoded as libpq decodes them.
func redactQuery(q string) string {
	pairs := strings.Split(q, "&")
	for i, pair := range pairs {
		if pair == "" {
			continue // libpq skips an empty parameter, as after a trailing &
		}
		raw, _, _ := strings.Cut(pair, "=")
		if name, err := url.PathUnescape(raw); err != nil || !slices.Contains(shownDatabaseParams, name) {
			pairs[i] = raw + "=" + secret.Redacted
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

// PoolConfig returns the connection settings that pgx read from the URL when
// it was parsed, with what the libpq environment variables and the files the
// URL names supplied then, so that the server connects with the settings the
// configuration checked. Each call returns a new copy, which the caller may
// change. It returns nil if the URL is unset.
func (d DatabaseURL) PoolConfig() *pgxpool.Config {
	if d.pool == nil {
		return nil
	}
	return d.pool.Copy()
}

// String returns the URL with its password, and the value of every query
// parameter that is not known to hold no secret, replaced by secret.Redacted.
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
