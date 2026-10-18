package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestCreateToken checks that create-token makes an API token for a member
// of a workspace, of the whole workspace or narrowed to one of its projects,
// with the scopes and the expiry that its flags give, prints its secret
// alone on standard output, which the database holds only as a hash, and
// records the token's making in the audit log without an actor; and that it
// refuses an account that is not a member, a project of another workspace,
// and flags that are not valid.
//
// Verifies: FR-ADMIN-06/AC1, FR-TEN-07/AC2
func TestCreateToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	env := databaseEnv(d)
	for _, email := range []string{"jo@example.com", "kim@example.com"} {
		if code, _, stderr := runWithInput(t, env, testPassword, "create-user", email, "Someone"); code != ExitOK {
			t.Fatalf("create-user %s: exit %d, stderr %q", email, code, stderr)
		}
	}
	admin := d.Admin(t)
	if _, err := admin.Exec(ctx, `
		INSERT INTO workspace (id, name, slug, plan_key) VALUES
			('0192f5c4-0000-7000-8000-0000000000aa', 'Harbour', 'harbour', 'unlimited'),
			('0192f5c4-0000-7000-8000-0000000000bb', 'Other', 'other', 'unlimited');
		INSERT INTO project (id, workspace_id, name) VALUES
			('0192f5c4-0000-7000-8000-0000000000cc', '0192f5c4-0000-7000-8000-0000000000aa', 'Lighthouse'),
			('0192f5c4-0000-7000-8000-0000000000dd', '0192f5c4-0000-7000-8000-0000000000bb', 'Elsewhere')`); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runWithInput(t, env, "", "grant-role", "jo@example.com", "viewer", "harbour"); code != ExitOK {
		t.Fatalf("grant-role: exit %d, stderr %q", code, stderr)
	}
	// The IDs of the projects, as the API writes them.
	encode := func(kind ids.Kind, id string) string {
		u, err := ids.ParseUUID(id)
		if err != nil {
			t.Fatal(err)
		}
		return ids.Encode(kind, u)
	}
	lighthouse := encode(ids.Project, "0192f5c4-0000-7000-8000-0000000000cc")
	elsewhere := encode(ids.Project, "0192f5c4-0000-7000-8000-0000000000dd")

	// The token holds each scope once, in the order of the PRD's table.
	code, stdout, stderr := runWithInput(t, env, "", "create-token", "--scopes",
		"project:write, snapshot:read,project:write", "--expires", "2099-01-31T00:00:00Z", "jo@example.com", "harbour",
		"CI")
	secret := strings.TrimSuffix(stdout, "\n")
	if code != ExitOK || !strings.HasPrefix(secret, "scp_") || strings.Contains(secret, "\n") ||
		!strings.Contains(stderr, "with the scopes snapshot:read, project:write") {
		t.Fatalf("create-token: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, "scp_"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	var got string
	if err := admin.QueryRow(ctx, `SELECT format('%s %s %s %s', name, scopes, project_id IS NULL,
		expires_at = '2099-01-31T00:00:00Z'::timestamptz) FROM api_token WHERE token_hash = $1`, sum[:]).Scan(&got); err !=
		nil || got != "CI {snapshot:read,project:write} t t" {
		t.Errorf("the token with the secret's hash is %q, %v", got, err)
	}

	code, stdout, stderr = runWithInput(t, env, "", "create-token", "--scopes", "project:read", "--project", lighthouse,
		"jo@example.com", "harbour", "Godot")
	if code != ExitOK || !strings.HasPrefix(stdout, "scp_") || !strings.Contains(stderr, lighthouse) {
		t.Errorf("a token narrowed to a project: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got := query(t, d, `SELECT format('%s %s', a.action, a.actor_user_id IS NULL) FROM audit_entry a
		WHERE a.action = 'token.create'`); !slices.Equal(got, []string{"token.create t", "token.create t"}) {
		t.Errorf("the audit log records %q", got)
	}

	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--scopes", "project:read", "kim@example.com", "harbour", "CI"}, ExitFailure,
			"kim@example.com is not a member of harbour"},
		{[]string{"--scopes", "project:read", "--project", elsewhere, "jo@example.com", "harbour", "CI"}, ExitFailure,
			"or " + elsewhere + " is no project of it"},
		{[]string{"--scopes", "project:read", "jo@example.com", "nowhere", "CI"}, ExitFailure,
			`no workspace has the slug "nowhere"`},
		{[]string{"--scopes", "project:read", "nobody@example.com", "harbour", "CI"}, ExitFailure,
			"the email address belongs to no account"},
		{[]string{"--scopes", "project:read", "jo@example.com", "harbour", " "}, ExitFailure, "the token's name "},
		{[]string{"--scopes", "project:read", "--expires", "2000-01-01T00:00:00Z", "jo@example.com", "harbour", "CI"},
			ExitFailure, "the token's expiry is not in the future"},
		{[]string{"--scopes", "project:read", "--expires", "0001-01-01T00:00:00Z", "jo@example.com", "harbour", "CI"},
			ExitFailure, "the token's expiry is not in the future"},
		{[]string{"--scopes", "project:admin", "jo@example.com", "harbour", "CI"}, ExitUsage,
			`unknown scope "project:admin"`},
		{[]string{"--scopes", " , ", "jo@example.com", "harbour", "CI"}, ExitUsage, "--scopes lists no scope"},
		{[]string{"jo@example.com", "harbour", "CI"}, ExitUsage, "--scopes lists no scope"},
		{[]string{"--scopes", "project:read", "--project", encode(ids.Workspace, "0192f5c4-0000-7000-8000-0000000000aa"),
			"jo@example.com", "harbour", "CI"}, ExitUsage, "is not a project's ID"},
		{[]string{"--scopes", "project:read", "--expires", "tomorrow", "jo@example.com", "harbour", "CI"}, ExitUsage,
			"is not a time in RFC 3339"},
		{[]string{"--scopes", "project:read", "jo@example.com", "harbour"}, ExitUsage,
			"expected an email address, a workspace's slug, and the token's name"},
	} {
		code, stdout, stderr := runWithInput(t, env, "", append([]string{"create-token"}, c.args...)...)
		if code != c.code || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("create-token %q: exit %d, stdout %q, stderr %q; want %d and %q", c.args, code, stdout, stderr,
				c.code, c.want)
		}
	}
	if n := query(t, d, "SELECT count(*)::text FROM api_token"); !slices.Equal(n, []string{"2"}) {
		t.Errorf("the refused commands left %v tokens, want 2", n)
	}
}
