package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

func TestParseDatabaseURL(t *testing.T) {
	tests := []struct {
		in, shown, user, database, password string
	}{
		{testDatabaseURL, testDatabaseShown, "storycreep", "storycreep", "pw-s3cr3t"},
		{"postgresql://app@db/sc", "postgresql://app@db/sc", "app", "sc", ""},
		{"postgres://app:@db/sc", "postgres://app@db/sc", "app", "sc", ""},
		{"postgres://app:p%40ss%3Aw%2Frd@[::1]:5433/sc?sslmode=verify-full&sslpassword=key-pw&application_name=x",
			"postgres://app:" + secret.Redacted + "@[::1]:5433/sc?sslmode=verify-full&sslpassword=" + secret.Redacted +
				"&application_name=x",
			"app", "sc", "p@ss:w/rd"},
		// A Unix socket directory instead of a host, in both of libpq's forms.
		{"postgres://app:pw@/sc?host=/var/run/postgresql", "postgres://app:" + secret.Redacted + "@/sc?host=/var/run/postgresql",
			"app", "sc", "pw"},
		{"postgres://app:pw@%2Fvar%2Frun%2Fpostgresql/sc", "postgres://app:" + secret.Redacted + "@%2Fvar%2Frun%2Fpostgresql/sc",
			"app", "sc", "pw"},
		// libpq reads a ; and a + in a value as they are.
		{"postgres://app@db/sc?application_name=a;b+c", "postgres://app@db/sc?application_name=a;b+c", "app", "sc", ""},
		{"postgres://sc%20app:pw@db/my%20db", "postgres://sc%20app:" + secret.Redacted + "@db/my%20db", "sc app", "my db", "pw"},
		// An @ after the path, and an = in a value written as %3D, are as
		// libpq reads them. So is a trailing &.
		{"postgres://app@db/sc?application_name=a@b&options=-c%20search_path%3Dx&",
			"postgres://app@db/sc?application_name=a@b&options=-c%20search_path%3Dx&", "app", "sc", ""},
	}
	for _, tt := range tests {
		d, err := ParseDatabaseURL(tt.in)
		if err != nil {
			t.Errorf("%s: %v", tt.in, err)
			continue
		}
		if d.String() != tt.shown || d.User() != tt.user || d.Database() != tt.database ||
			d.Password().Reveal() != tt.password || d.Reveal() != tt.in || !d.IsSet() {
			t.Errorf("%s: got %s, user %q, database %q, password %q, revealed %q", tt.in, d, d.User(), d.Database(),
				d.Password().Reveal(), d.Reveal())
		}
	}
	if (DatabaseURL{}).IsSet() || (DatabaseURL{}).PoolConfig() != nil {
		t.Errorf("the zero DatabaseURL is set, or has a pool configuration")
	}

	// The pool configuration is what pgx read, and each caller gets a copy
	// of its own.
	d, err := ParseDatabaseURL("postgres://app:pw@%2Fvar%2Frun%2Fpostgresql/sc?application_name=a;b+c")
	if err != nil {
		t.Fatal(err)
	}
	cfg := d.PoolConfig()
	if cfg.ConnConfig.Host != "/var/run/postgresql" || cfg.ConnConfig.Password != "pw" ||
		cfg.ConnConfig.RuntimeParams["application_name"] != "a;b+c" {
		t.Errorf("pool configuration: host %q, application_name %q", cfg.ConnConfig.Host,
			cfg.ConnConfig.RuntimeParams["application_name"])
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "changed"
	if got := d.PoolConfig().ConnConfig.RuntimeParams["application_name"]; got != "a;b+c" {
		t.Errorf("a change to one copy reached the next: application_name %q", got)
	}
}

// TestInvalidDatabaseURL checks that each invalid URL is rejected with an
// explanation that never repeats the URL, which may hold a password.
//
// Verifies: NFR-SEC-14
func TestInvalidDatabaseURL(t *testing.T) {
	const password = "pw-s3cr3t"
	tests := []struct{ in, want string }{
		{"db.example.com", "must start with postgres://"},
		{"host=db user=app password=" + password, "must start with postgres://"},
		{"mysql://app:" + password + "@db/sc", "must start with postgres://"},
		{"postgres:app:" + password, "must start with postgres://"},
		// pgx would read these as keyword=value settings and ignore the URL.
		{"POSTGRES://app:" + password + "@db/sc", "in lowercase"},
		{"Postgresql://app:" + password + "@db/sc?sslmode=require", "in lowercase"},
		{"postgres://:" + password + "@db/sc", "names no user"},
		{"postgres://db/sc", "names no user"},
		{"postgres://app:" + password + "@db", "names no database"},
		{"postgres://app:" + password + "@db/", "names no database"},
		{"postgres://app:" + password + "@db/sc/extra", "only the database name"},
		{"postgres://app:" + password + "@db/sc#" + password, "fragment"},
		// libpq would read the # as part of the database name.
		{"postgres://app:" + password + "@db/sc#", "fragment"},
		{"postgres://app:%zz" + password + "@db/sc", "not a valid URL"},
		{"postgres://app:" + password + "@db/sc?sslmode=%zz", "the query is not valid"},
		// pgx, like libpq, ends the password at the first @, and would log in
		// with "pw" at the host "<password>@db".
		{"postgres://app:pw@" + password + "@db/sc", "not percent-encoded; write it as %40"},
		{"postgres://app@" + password + "@db/sc?sslmode=require", "not percent-encoded"},
		// libpq reads a ? or # before the @ as part of the password, where
		// url.Parse would read a query or a fragment.
		{"postgres://app:pw#" + password + "@db/sc", "has a ? or # in its user or password that is not percent-encoded"},
		{"postgres://app:12?" + password + "@db/sc", "has a ? or # in its user or password"},
		// libpq, and pgx with it, refuses these, which url.ParseQuery accepts.
		{"postgres://app:" + password + "@db/sc?options=-c%20search_path=x", "percent-encoded as %3D"},
		{"postgres://app:" + password + "@db/sc?sslmode", "must be name=value"},
		{"postgres://app:" + password + "@db/sc?sslmode=require&&application_name=x", "must be name=value"},
		{"postgres://app@db/sc?password=" + password, "must not set password"},
		{"postgres://app:" + password + "@db/sc?user=other", "must not set user"},
		{"postgres://app:" + password + "@db/sc?dbname=other", "must not set dbname"},
		{"postgres://app:" + password + "@db/sc?database=other", "must not set database"},
		// libpq skips the spaces around a name, so these would set the
		// password and the key's password, under names that the checks and
		// the redaction would not recognize.
		{"postgres://app@db/sc?sslmode=disable& password=" + password, "has a space"},
		{"postgres://app:pw@db/sc?sslpassword =" + password, "has a space"},
		{"postgres://app:pw@db/sc?application_name=a\x7f" + password, "control character"},
		// pgx, which the server connects with, refuses these, which the
		// checks above accept.
		{"postgres://app:" + password + "@db/sc?sslmode=required", "sslmode is invalid"},
		{"postgres://app:" + password + "@db/sc?pool_max_conns=many", "cannot parse pool_max_conns"},
		{"postgres://app:" + password + "@db/my db", "has a space"},
		{"postgres://app:" + password + "@db:0/sc", "invalid port"},
		{"postgres://app:" + password + "@db/sc?sslmode=verify-full&sslrootcert=no-such-root.crt", "unable to read CA file"},
	}
	for _, tt := range tests {
		for _, key := range []string{"STORYCREEP_DATABASE_URL", "STORYCREEP_DATABASE_ADMIN_URL"} {
			problems := loadProblems(t, Options{Env: MapEnvironment(withRequired(map[string]string{key: tt.in}))})
			if len(problems) != 1 || problems[0].Key != key {
				t.Errorf("%s=%s: problems = %v, want one for %s", key, tt.in, problems, key)
				continue
			}
			msg := problems[0].String()
			if !strings.Contains(msg, tt.want) {
				t.Errorf("%s=%s: message %q should say %q", key, tt.in, msg, tt.want)
			}
			// pgx's own errors quote the URL between backticks.
			if strings.Contains(msg, password) || strings.Contains(msg, "zz") || strings.Contains(msg, "`") {
				t.Errorf("%s=%s: the message repeats the URL: %s", key, tt.in, msg)
			}
		}
	}
}

// TestDatabaseURLIsRedacted checks that every way of printing or encoding a
// database URL hides its password.
//
// Verifies: NFR-SEC-14
func TestDatabaseURLIsRedacted(t *testing.T) {
	c := mustLoad(t, withRequired(map[string]string{
		"STORYCREEP_DATABASE_ADMIN_URL": "postgres://postgres:admin-s3cr3t@db/storycreep",
	}))
	for _, key := range []string{"STORYCREEP_DATABASE_URL", "STORYCREEP_DATABASE_ADMIN_URL"} {
		if e := entry(t, c, key); !strings.Contains(e.Value, secret.Redacted) || strings.Contains(e.Value, "s3cr3t") {
			t.Errorf("entry %s = %q, want the password redacted", key, e.Value)
		}
	}

	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("x", "url", c.DatabaseAdminURL)
	encoded, err := json.Marshal(c.DatabaseAdminURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		fmt.Sprint(c.DatabaseAdminURL), fmt.Sprintf("%v %+v %#v %s %q", c.DatabaseAdminURL, c.DatabaseAdminURL,
			c.DatabaseAdminURL, c.DatabaseAdminURL, c.DatabaseAdminURL),
		fmt.Sprintf("%+v %#v", *c, *c), logged.String(), string(encoded),
	} {
		if strings.Contains(s, "s3cr3t") {
			t.Errorf("the password leaks: %s", s)
		}
	}
}

// TestAdminLoginDiffers checks that the two database URLs cannot name the
// same login, because the server's login must not be the superuser.
func TestAdminLoginDiffers(t *testing.T) {
	problems := loadProblems(t, Options{Env: MapEnvironment(withRequired(map[string]string{
		"STORYCREEP_DATABASE_ADMIN_URL": "postgres://storycreep:other@db/storycreep",
	}))})
	if len(problems) != 1 || problems[0].Key != "STORYCREEP_DATABASE_ADMIN_URL" ||
		!strings.Contains(problems[0].Msg, "same login") || strings.Contains(problems[0].Msg, "other") {
		t.Errorf("problems = %v, want one saying the logins must differ, without the URL", problems)
	}
}

// TestAdminDatabaseMatches checks that the admin URL must name the server's
// database, so that migrations are not applied to another one, such as the
// postgres database a superuser URL often names.
func TestAdminDatabaseMatches(t *testing.T) {
	problems := loadProblems(t, Options{Env: MapEnvironment(withRequired(map[string]string{
		"STORYCREEP_DATABASE_ADMIN_URL": "postgres://postgres:other@db/postgres",
	}))})
	if len(problems) != 1 || problems[0].Key != "STORYCREEP_DATABASE_ADMIN_URL" ||
		!strings.Contains(problems[0].Msg, "different database") || strings.Contains(problems[0].Msg, "other") {
		t.Errorf("problems = %v, want one saying the databases must match, without the URL", problems)
	}
	// Another host for the same database is allowed, as when the server
	// connects through a pooler and migrations connect directly.
	mustLoad(t, withRequired(map[string]string{
		"STORYCREEP_DATABASE_ADMIN_URL": "postgres://postgres:other@db-direct:5432/storycreep",
	}))
}
