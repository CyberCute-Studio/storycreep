package maintenance

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/personaldata"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

func open(t *testing.T, url config.DatabaseURL) *DB {
	t.Helper()
	db, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// TestOnlyMaintenanceBypassesRowLevelSecurity checks that after migrating,
// storycreep_maintenance is the only role with BYPASSRLS, apart from the
// superuser that applies the migrations (PRD §5.4). It reads every role of
// the test server, so it does not run in parallel with the tests that create
// roles.
func TestOnlyMaintenanceBypassesRowLevelSecurity(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	rows, err := d.Admin(t).Query(ctx, "SELECT rolname FROM pg_roles WHERE rolbypassrls OR rolsuper ORDER BY rolname")
	var got []string
	if err == nil {
		got, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{d.AdminURL.User(), database.MaintenanceRole}; !slices.Equal(got, want) {
		t.Errorf("the roles that bypass row-level security are %v, want %v", got, want)
	}
}

// TestInTxBypassesRowLevelSecurity checks that the maintenance path's
// transactions act as storycreep_maintenance for the maintenance login, and
// see the rows of every workspace, where the application login's see only
// their own workspace's.
func TestInTxBypassesRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	f := isolationtest.New(t, d, isolationtest.Tables)
	db := open(t, d.MaintenanceURL)

	memberships := func(tx pgx.Tx) []ids.UUID {
		t.Helper()
		rows, err := tx.Query(ctx, "SELECT id FROM membership ORDER BY id")
		var got []ids.UUID
		if err == nil {
			got, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
		}
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	both := []ids.UUID{f.A.Rows["membership"], f.B.Rows["membership"]}
	slices.SortFunc(both, func(a, b ids.UUID) int { return strings.Compare(a.String(), b.String()) })
	err := db.inTx(ctx, func(tx pgx.Tx) error {
		var session, current string
		if err := tx.QueryRow(ctx, "SELECT session_user, current_user").Scan(&session, &current); err != nil {
			return err
		}
		if session != dbtest.MaintenanceLogin || current != database.MaintenanceRole {
			t.Errorf("the transaction's session user is %s and current user %s, want %s and %s",
				session, current, dbtest.MaintenanceLogin, database.MaintenanceRole)
		}
		if got := memberships(tx); !slices.Equal(got, both) {
			t.Errorf("the maintenance path sees the memberships %v, want both workspaces' %v", got, both)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = f.DB.InTx(ctx, f.A.Scope(), func(tx pgx.Tx) error {
		if got := memberships(tx); !slices.Equal(got, []ids.UUID{f.A.Rows["membership"]}) {
			t.Errorf("the application login sees the memberships %v, want only workspace A's", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOpenRefusesOtherLogins checks that Open refuses a login that is not the
// maintenance login that migrating provisions: one that has more privileges
// than storycreep_maintenance gives it, or that cannot act as that role.
func TestOpenRefusesOtherLogins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)

	if _, err := Open(ctx, config.DatabaseURL{}); !errors.Is(err, ErrNoURL) {
		t.Errorf("Open without a URL returned %v, want ErrNoURL", err)
	}

	stranger := d.NewLogin(t, "")
	withBypass := d.NewLogin(t, "")
	bypass := pgx.Identifier{withBypass.User()}.Sanitize()
	if _, err := admin.Exec(ctx, "GRANT storycreep_maintenance TO "+bypass+"; ALTER ROLE "+bypass+" BYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		url    config.DatabaseURL
		unsafe bool
		want   string
	}{
		{"the application login", d.AppURL, true,
			`it is a member of "storycreep_app", which is reserved for the server's other work`},
		{"the superuser", d.AdminURL, true, "it is a superuser"},
		{"a login with BYPASSRLS", withBypass, true, "it has BYPASSRLS"},
		{"a login without the role", stranger, false, "cannot act as storycreep_maintenance"},
	} {
		_, err := Open(ctx, tt.url)
		var uerr *database.UnsafeLoginError
		if err == nil || errors.As(err, &uerr) != tt.unsafe || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Open returned %v, want an error saying %q", tt.name, err, tt.want)
			continue
		}
		if tt.unsafe && (uerr.Key != "STORYCREEP_DATABASE_MAINTENANCE_URL" ||
			!strings.Contains(err.Error(), "must be an ordinary role that bypasses row-level security only by "+
				"acting as storycreep_maintenance")) {
			t.Errorf("%s: Open returned %v, which does not name the maintenance login's key and rule", tt.name, err)
		}
	}
}

// TestAttributionReadsTheInventorysAlone checks that the personal-data
// export's read of attribution runs for each of the inventory's attributions,
// and refuses any other table or column, so that no caller reads another
// table of every workspace through it (PRD §5.4).
func TestAttributionReadsTheInventorysAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open(t, dbtest.New(t).MaintenanceURL)
	for _, a := range personaldata.Attributions() {
		if _, err := db.Attribution(ctx, ids.New(), a, ids.UUID{}, 10); err != nil {
			t.Errorf("%s.%s: %v", a.Table, a.Column, err)
		}
	}
	other := personaldata.Attribution{Table: "session", Column: "user_id", Relation: "created", At: "created_at",
		Project: "id"}
	if _, err := db.Attribution(ctx, ids.New(), other, ids.UUID{}, 10); !errors.Is(err, errNoAttribution) {
		t.Errorf("a read of session.user_id gives %v, want %v", err, errNoAttribution)
	}
}

// TestHoldingsSubjects checks which memberships and API tokens the entries
// about a user are found by: their own, and those that are gone, whose
// removal, whose creation by the operator, whose creation by the user's
// acceptance of an invitation, or whose workspace's creation by the user
// names the user, and never the membership of another member whom the user
// acted on, as an owner or an admin does.
func TestHoldingsSubjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	db := open(t, d.MaintenanceURL)
	user, other, ws := ids.New(), ids.New(), ids.New()
	for _, u := range []ids.UUID{user, other} {
		if _, err := admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, 'Someone')`, u,
			u.String()+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, 'free')`,
		ws, "harbour-"+ws.String()[:8]); err != nil {
		t.Fatal(err)
	}
	current := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role)
		VALUES ($1, $2, $3, 'editor')`, current, ws, user); err != nil {
		t.Fatal(err)
	}
	removed, granted, joined, created, token := ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	actedOn, othersGranted := ids.New(), ids.New()
	entry := func(action, kind string, subject ids.UUID, actor *ids.UUID, metadata string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `INSERT INTO audit_entry (id, workspace_id, actor_user_id, action, subject_kind,
			subject_id, result, metadata) VALUES ($1, $2, $3, $4, $5, $6, 'success', $7)`, ids.New(), ids.New(), actor,
			action, kind, subject, metadata); err != nil {
			t.Fatal(err)
		}
	}
	u := user.String()
	entry("membership.remove", "membership", removed, &other, `{"role": "viewer", "user_id": "`+u+`"}`)
	entry("membership.create", "membership", granted, nil, `{"role": "viewer", "user_id": "`+u+`"}`)
	entry("membership.create", "membership", joined, &user, `{"role": "viewer"}`)
	entry("workspace.create", "workspace", ids.New(), &user, `{"personal": true, "membership_id": "`+created.String()+`"}`)
	entry("token.create", "api_token", token, &user, `{"user_id": "`+u+`"}`)
	// Another member's membership, whose role the user changed, and one that
	// the operator made for the other member.
	entry("membership.role_change", "membership", actedOn, &user, `{"role": "viewer", "previous_role": "editor"}`)
	entry("membership.create", "membership", othersGranted, nil, `{"role": "viewer", "user_id": "`+other.String()+`"}`)

	h, err := db.Holdings(ctx, user, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []ids.UUID{current, removed, granted, joined, created, token}
	slices.SortFunc(want, func(a, b ids.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(h.Subjects, want) {
		t.Errorf("the subjects are %v, want %v", h.Subjects, want)
	}
}

// TestActedOnPages checks that the entries about a user and their subjects,
// whose actor is someone else or nobody, are read a page at a time, in the
// order in which they were made, an entry's ID breaking a tie, whole and
// once, however the subjects' entries interleave, and that the user's own
// entries, and those about another subject, are never among them.
func TestActedOnPages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	db := open(t, d.MaintenanceURL)
	user, other, membership, token, elsewhere := ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	actions := map[string]string{"user": "auth.login", "membership": "membership.role_change",
		"api_token": "token.revoke"}
	var want []ids.UUID
	add := func(minute int, kind string, subject ids.UUID, actor *ids.UUID, about bool) {
		t.Helper()
		id := ids.New()
		if _, err := admin.Exec(ctx, `INSERT INTO audit_entry (id, actor_user_id, action, subject_kind, subject_id,
			result, created_at) VALUES ($1, $2, $3, $4, $5, 'success', $6)`, id, actor, actions[kind], kind, subject,
			start.Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if about {
			want = append(want, id)
		}
	}
	for i := range 7 {
		add(i, "user", user, nil, true)
		add(i, "membership", membership, &other, true)
		add(i, "user", user, &user, false)
		add(i, "membership", elsewhere, &other, false)
	}
	add(3, "api_token", token, &other, true)
	add(10, "user", user, nil, false) // after until
	got, after := []ids.UUID{}, personaldata.AuditCursor{}
	for {
		page, err := db.ActedOn(ctx, user, []ids.UUID{membership, token}, start.Add(9*time.Minute), after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			got = append(got, e.ID)
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].Cursor()
	}
	if want = sortedByMade(t, admin, want); !slices.Equal(got, want) {
		t.Errorf("the pages hold %v, want %v", got, want)
	}
}

// sortedByMade returns the audit entries list in the order in which they were
// made, then by ID.
func sortedByMade(t *testing.T, conn *pgx.Conn, list []ids.UUID) []ids.UUID {
	t.Helper()
	rows, err := conn.Query(context.Background(), `SELECT id FROM audit_entry WHERE id = ANY($1)
		ORDER BY created_at, id`, list)
	var out []ids.UUID
	if err == nil {
		out, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
	}
	if err != nil {
		t.Fatal(err)
	}
	return out
}
