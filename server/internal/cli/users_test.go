package cli

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

const testPassword = "correct horse battery staple"

// runWithInput runs the command line args, with stdin as its standard input.
func runWithInput(t *testing.T, env map[string]string, stdin string, args ...string) (code int, stdout,
	stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, strings.NewReader(stdin), &out, &errOut, config.MapEnvironment(env))
	return code, out.String(), errOut.String()
}

// databaseEnv returns a configuration for the database d.
func databaseEnv(d *dbtest.Database) map[string]string {
	return map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_DATABASE_URL": d.AppURL.Reveal()}
}

// query returns the rows of a query as the superuser, each as one text.
func query(t *testing.T, d *dbtest.Database, sql string) []string {
	t.Helper()
	rows, err := d.Admin(t).Query(context.Background(), sql)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// accountsOf describes the accounts of d.
func accountsOf(t *testing.T, d *dbtest.Database) []string {
	t.Helper()
	return query(t, d, `SELECT format('%s %s admin=%s', email, display_name, is_instance_admin) FROM "user"
		ORDER BY email`)
}

// auditOf describes the audit entries of d, each with the email address of
// its subject, or of its subject membership's account.
func auditOf(t *testing.T, d *dbtest.Database) []string {
	t.Helper()
	return query(t, d, `
		SELECT format('%s %s %s %s actor=%s', a.action, coalesce(u.email, mu.email), a.result, a.metadata,
			a.actor_user_id)
		FROM audit_entry a
		LEFT JOIN "user" u ON u.id = a.subject_id
		LEFT JOIN membership m ON m.id = a.subject_id
		LEFT JOIN "user" mu ON mu.id = m.user_id
		WHERE a.action <> 'workspace.create'
		ORDER BY a.id`)
}

// TestCreateUser checks that create-user creates an account with the
// password of its standard input's first line, whatever the signup policy
// and before setup has completed, and an instance admin with
// --instance-admin, which the audit log records.
//
// Verifies: FR-ADMIN-06/AC1
func TestCreateUser(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	env := databaseEnv(d)

	code, stdout, stderr := runWithInput(t, env, testPassword+"\r\nignored\n", "create-user", " jo@example.com ",
		"Jo Novak")
	if code != ExitOK || !strings.HasPrefix(stdout, "Created the account jo@example.com, with the ID usr_") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithInput(t, env, testPassword, "create-user", "--instance-admin", "admin@example.com",
		"Ada Admin")
	if code != ExitOK || !strings.HasPrefix(stdout, "Created the instance admin's account admin@example.com, ") {
		t.Errorf("--instance-admin: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	want := []string{"admin@example.com Ada Admin admin=t", "jo@example.com Jo Novak admin=f"}
	if got := accountsOf(t, d); !slices.Equal(got, want) {
		t.Errorf("the accounts are %q, want %q", got, want)
	}
	if got := auditOf(t, d); !slices.Equal(got, []string{"instance.admin_grant admin@example.com success {} actor="}) {
		t.Errorf("the audit log holds %q", got)
	}
	var matches bool
	if err := d.Admin(t).QueryRow(context.Background(), `SELECT password_hash LIKE '$argon2id$%'
		FROM "user" WHERE email = 'jo@example.com'`).Scan(&matches); err != nil || !matches {
		t.Errorf("the password's hash is not argon2id: %v", err)
	}
	if got := query(t, d, "SELECT (setup_completed_at IS NULL)::text FROM instance"); !slices.Equal(got,
		[]string{"true"}) {
		t.Error("creating accounts completed first-run setup")
	}
}

// TestCreateUserFailures checks that create-user exits with status 1, saying
// why, and creates no account, when the password is missing or weak, the
// address or the display name is not valid, or the address has an account,
// and with status 2 when the command line is wrong.
//
// Verifies: FR-ADMIN-06/AC1
func TestCreateUserFailures(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	env := databaseEnv(d)
	if code, _, stderr := runWithInput(t, env, testPassword, "create-user", "taken@example.com", "Taken"); code !=
		ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, c := range []struct {
		name, stdin string
		args        []string
		code        int
		want        string
	}{
		{"no password", "", []string{"jo@example.com", "Jo"}, ExitFailure, "standard input holds no password"},
		{"an empty line", "\n" + testPassword, []string{"jo@example.com", "Jo"}, ExitFailure,
			"standard input holds no password"},
		{"a short password", "short", []string{"jo@example.com", "Jo"}, ExitFailure,
			"the password has fewer than 12 characters"},
		{"a common password", "Unbelievable", []string{"jo@example.com", "Jo"}, ExitFailure,
			"the password is one of the most common passwords"},
		{"a long password", strings.Repeat("x", 257), []string{"jo@example.com", "Jo"}, ExitFailure,
			"the password has more than 256 characters"},
		// Standard input is read up to a limit, which cuts a character of
		// four bytes in two here.
		{"a long password of wide characters", strings.Repeat("\U0001F600", 300), []string{"jo@example.com", "Jo"},
			ExitFailure, "the password has more than 256 characters"},
		{"a password that is not UTF-8", "\xff\xfe" + testPassword, []string{"jo@example.com", "Jo"}, ExitFailure,
			"the password is not UTF-8 text"},
		{"fields", testPassword, []string{"jo", " "}, ExitFailure,
			"the email address is not an email address; the display name is empty"},
		{"an address that has an account", testPassword, []string{"TAKEN@example.com", "Jo"}, ExitFailure,
			"the email address has an account already"},
		{"no name", testPassword, []string{"jo@example.com"}, ExitUsage, "expected an email address and a display name"},
		{"too many", testPassword, []string{"jo@example.com", "Jo", "Novak"}, ExitUsage,
			"expected an email address and a display name"},
	} {
		code, stdout, stderr := runWithInput(t, env, c.stdin, append([]string{"create-user"}, c.args...)...)
		if code != c.code || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d and %q", c.name, code, stdout, stderr, c.code, c.want)
		}
		if strings.Contains(stderr, testPassword) {
			t.Errorf("%s: stderr shows the password: %q", c.name, stderr)
		}
	}
	if got := accountsOf(t, d); len(got) != 1 {
		t.Errorf("the accounts are %q, want only the one that was there", got)
	}
}

// TestUserCommandsNeedMigrations checks that create-user and grant-role
// refuse a database with pending migrations.
//
// Verifies: FR-ADMIN-06/AC1
func TestUserCommandsNeedMigrations(t *testing.T) {
	t.Parallel()
	d := dbtest.Empty(t)
	if code, _, stderr := run(t, migrateEnv(d, true), "migrate"); code != ExitOK {
		t.Fatalf("migrate: exit %d, stderr %q", code, stderr)
	}
	if _, err := d.Admin(t).Exec(context.Background(),
		"DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"create-user", "jo@example.com", "Jo"},
		{"grant-role", "jo@example.com", "instance-admin"},
		{"reset-password", "jo@example.com"},
	} {
		code, _, stderr := runWithInput(t, databaseEnv(d), testPassword, args...)
		if code != ExitFailure || !strings.Contains(stderr, "the database has pending migrations") {
			t.Errorf("%s: exit %d, stderr %q", args[0], code, stderr)
		}
	}
}

// TestResetPassword checks that reset-password prints a link that resets the
// account's password, for an hour, on the instance's URL, which the audit log
// records without an actor, and that it fails for an address of no active
// account.
//
// Verifies: FR-ADMIN-06/AC1, FR-AUTH-03/AC1
func TestResetPassword(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	env := databaseEnv(d)
	if code, _, stderr := runWithInput(t, env, testPassword, "create-user", "jo@example.com", "Jo"); code != ExitOK {
		t.Fatalf("create-user: exit %d, stderr %q", code, stderr)
	}
	code, stdout, stderr := run(t, env, "reset-password", "JO@example.com")
	if code != ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	_, link, ok := strings.Cut(stdout, "\n\n  ")
	if !ok || !strings.HasPrefix(link, testURL+"/app/reset-password#token=") {
		t.Fatalf("the output holds no link: %q", stdout)
	}
	if got := query(t, d, `SELECT format('%s %s hash=%s hour=%s', t.kind, u.email, t.token_hash IS NOT NULL,
		t.expires_at BETWEEN now() + interval '59 minutes' AND now() + interval '1 hour')
		FROM email_token t JOIN "user" u ON u.id = t.user_id`); !slices.Equal(got,
		[]string{"password_reset jo@example.com hash=t hour=t"}) {
		t.Errorf("the tokens are %q", got)
	}
	if got := auditOf(t, d); !slices.Contains(got,
		`auth.link_issue jo@example.com success {"kind": "password_reset"} actor=`) {
		t.Errorf("the audit log is %q", got)
	}
	if _, err := d.Admin(t).Exec(context.Background(), `UPDATE "user" SET status = 'suspended'`); err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]string{
		"nobody@example.com": "the email address belongs to no account",
		"jo@example.com":     "the email address belongs to an account that is not active",
	} {
		code, stdout, stderr := run(t, env, "reset-password", email)
		if code != ExitFailure || stdout != "" || !strings.Contains(stderr, want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", email, code, stdout, stderr)
		}
	}
	if code, _, stderr := run(t, env, "reset-password"); code != ExitUsage || !strings.Contains(stderr,
		"expected an email address") {
		t.Errorf("without an address: exit %d, stderr %q", code, stderr)
	}
}

// TestGrantRole checks that grant-role makes an account a member of a
// workspace with a role, changes the role, keeps the workspace's last
// owner, and makes an account an instance admin and no longer one, that it
// refuses an account that is not active, but to revoke instance admin from
// it, and that the audit log records each change.
//
// Verifies: FR-ADMIN-06/AC1, FR-TEN-07/AC2
func TestGrantRole(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	env := databaseEnv(d)
	for _, email := range []string{"owner@example.com", "jo@example.com"} {
		if code, _, stderr := runWithInput(t, env, testPassword, "create-user", email, "Someone"); code != ExitOK {
			t.Fatalf("create-user %s: exit %d, stderr %q", email, code, stderr)
		}
	}
	if code, _, stderr := runWithInput(t, env, "", "grant-role", "owner@example.com", "owner", "harbour"); code !=
		ExitFailure || !strings.Contains(stderr, `no workspace has the slug "harbour"`) {
		t.Errorf("an unknown workspace: exit %d, stderr %q", code, stderr)
	}
	if _, err := d.Admin(t).Exec(context.Background(), `
		INSERT INTO workspace (id, name, slug, plan_key) VALUES ('0192f5c4-0000-7000-8000-0000000000aa', 'Harbour',
			'harbour', 'unlimited')`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"owner@example.com", "owner", "harbour"}, ExitOK,
			"owner@example.com is now a member of harbour, as an owner.\n"},
		{[]string{"jo@example.com", "viewer", "harbour"}, ExitOK,
			"jo@example.com is now a member of harbour, as a viewer.\n"},
		{[]string{"JO@example.com", "editor", "harbour"}, ExitOK,
			"JO@example.com is now an editor of harbour, instead of a viewer.\n"},
		{[]string{"jo@example.com", "editor", "harbour"}, ExitOK, "jo@example.com is an editor of harbour already.\n"},
		{[]string{"owner@example.com", "admin", "harbour"}, ExitFailure,
			"owner@example.com is the last owner of harbour, which must keep one"},
		{[]string{"jo@example.com", "owner", "harbour"}, ExitOK,
			"jo@example.com is now an owner of harbour, instead of an editor.\n"},
		{[]string{"owner@example.com", "admin", "harbour"}, ExitOK,
			"owner@example.com is now an admin of harbour, instead of an owner.\n"},
		{[]string{"jo@example.com", "instance-admin"}, ExitOK, "jo@example.com is now an instance admin.\n"},
		{[]string{"jo@example.com", "instance-admin"}, ExitOK, "jo@example.com is an instance admin already.\n"},
		{[]string{"--revoke", "jo@example.com", "instance-admin"}, ExitOK,
			"jo@example.com is no longer an instance admin.\n"},
		{[]string{"--revoke", "jo@example.com", "instance-admin"}, ExitOK,
			"jo@example.com is not an instance admin.\n"},
		{[]string{"nobody@example.com", "viewer", "harbour"}, ExitFailure, "the email address belongs to no account"},
		{[]string{"nobody@example.com", "instance-admin"}, ExitFailure, "the email address belongs to no account"},
		{[]string{"jo@example.com", "member", "harbour"}, ExitUsage, `unknown role "member"`},
		{[]string{"jo@example.com", "viewer"}, ExitUsage, "the role viewer is a workspace's, so it needs"},
		{[]string{"jo@example.com", "instance-admin", "harbour"}, ExitUsage, "takes no workspace"},
		{[]string{"--revoke", "jo@example.com", "viewer", "harbour"}, ExitUsage, "--revoke takes only the role"},
		{[]string{"jo@example.com"}, ExitUsage, "expected an email address, a role"},
	} {
		code, stdout, stderr := runWithInput(t, env, "", append([]string{"grant-role"}, c.args...)...)
		out := stdout
		if c.code != ExitOK {
			out = stderr
		}
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("%q: exit %d, stdout %q, stderr %q; want %d and %q", c.args, code, stdout, stderr, c.code,
				c.want)
		}
	}
	// A suspended instance admin keeps the role until it is revoked, which
	// needs no active account.
	if _, err := d.Admin(t).Exec(context.Background(),
		`UPDATE "user" SET status = 'suspended', is_instance_admin = true WHERE email = 'owner@example.com'`); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"owner@example.com", "viewer", "harbour"}, {"owner@example.com", "instance-admin"}} {
		code, _, stderr := runWithInput(t, env, "", append([]string{"grant-role"}, args...)...)
		if code != ExitFailure || !strings.Contains(stderr, "belongs to an account that is not active") {
			t.Errorf("%q of a suspended account: exit %d, stderr %q", args, code, stderr)
		}
	}
	code, stdout, stderr := runWithInput(t, env, "", "grant-role", "--revoke", "owner@example.com", "instance-admin")
	if code != ExitOK || stdout != "owner@example.com is no longer an instance admin.\n" {
		t.Errorf("--revoke of a suspended instance admin: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	members := query(t, d, `SELECT format('%s %s', u.email, m.role) FROM membership m JOIN "user" u ON u.id = m.user_id
		ORDER BY u.email`)
	if want := []string{"jo@example.com owner", "owner@example.com admin"}; !slices.Equal(members, want) {
		t.Errorf("the members are %q, want %q", members, want)
	}
	want := []string{
		`membership.create owner@example.com success {"role": "owner"} actor=`,
		`membership.create jo@example.com success {"role": "viewer"} actor=`,
		`membership.role_change jo@example.com success {"role": "editor", "previous_role": "viewer"} actor=`,
		`membership.role_change jo@example.com success {"role": "owner", "previous_role": "editor"} actor=`,
		`membership.role_change owner@example.com success {"role": "admin", "previous_role": "owner"} actor=`,
		`instance.admin_grant jo@example.com success {} actor=`,
		`instance.admin_revoke jo@example.com success {} actor=`,
		`instance.admin_revoke owner@example.com success {} actor=`,
	}
	if got := auditOf(t, d); !slices.Equal(got, want) {
		t.Errorf("the audit log holds\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestUserCommandsHelp checks the help of create-user, grant-role, and
// reset-password.
//
// Verifies: FR-ADMIN-06/AC1
func TestUserCommandsHelp(t *testing.T) {
	for name, want := range map[string]string{
		"create-user":    "Usage: storycreep create-user [flags] <email> <display-name>\n",
		"grant-role":     "Usage: storycreep grant-role [flags] <email> <role> [workspace]\n",
		"reset-password": "Usage: storycreep reset-password [flags] <email>\n",
	} {
		code, stdout, stderr := run(t, nil, name, "--help")
		if code != ExitOK || !strings.HasPrefix(stdout, want) || stderr != "" {
			t.Errorf("%s --help: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "STORYCREEP_DATABASE_URL") {
			t.Errorf("%s --help does not say which database it connects to:\n%s", name, stdout)
		}
	}
	code, stdout, _ := run(t, nil, "--help")
	for _, name := range []string{"create-user", "grant-role", "reset-password"} {
		if code != ExitOK || !strings.Contains(stdout, fmt.Sprintf("\n  %s ", name)) {
			t.Errorf("the command list lacks %s:\n%s", name, stdout)
		}
	}
}
