package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// The events that only the tests record: actionChange belongs to a
// workspace, is about a project, and has metadata, as no registered event has
// yet, and actionBare has no subject.
const (
	actionChange Action = "test.change"
	actionBare   Action = "test.bare"
)

func init() {
	events = append(events,
		eventSpec{action: actionChange, inWorkspace: true, subject: "project",
			results: []Result{ResultSuccess, ResultDenied}, metadata: []string{"rows", "note"}},
		eventSpec{action: actionBare, inWorkspace: true, results: []Result{ResultSuccess}})
}

// errRolledBack rolls back a transaction whose statements worked.
var errRolledBack = errors.New("rolled back")

// isDenied reports whether err is PostgreSQL's refusal for want of a
// privilege, which is also how row-level security refuses a row.
func isDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// stored is an entry as the superuser reads it.
type stored struct {
	workspaceID, actorUserID, actorTokenID, subjectID pgtype.UUID
	action, result                                    string
	subjectKind, userAgent, requestID                 pgtype.Text
	ip                                                *netip.Addr
	metadata                                          map[string]any
	// text is the whole row as text, in which a test looks for what an entry
	// must not hold.
	text string
}

// entries returns, as the superuser, the entries that match the condition
// where on the audit_entry a, in the order in which they were added, by
// transactions that did not overlap.
func entries(t *testing.T, admin *pgx.Conn, where string, args ...any) []stored {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT workspace_id, actor_user_id, actor_token_id, subject_id, action, result, subject_kind, user_agent,
			request_id, ip, metadata, a::text
		FROM audit_entry a WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stored, error) {
		var s stored
		err := r.Scan(&s.workspaceID, &s.actorUserID, &s.actorTokenID, &s.subjectID, &s.action, &s.result,
			&s.subjectKind, &s.userAgent, &s.requestID, &s.ip, &s.metadata, &s.text)
		return s, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// addProject adds a project to the workspace of tx, the change that the
// tests record, and returns its ID.
func addProject(t *testing.T, tx pgx.Tx, workspace ids.UUID) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := tx.Exec(context.Background(), "INSERT INTO project (id, workspace_id) VALUES ($1, $2)", id,
		workspace); err != nil {
		t.Fatalf("add a project: %v", err)
	}
	return id
}

// TestRecord checks what Record stores: an entry of a workspace with every
// field, whose request ID is the context's, and one of no workspace, which
// has neither a workspace nor, without a subject, a subject's kind.
func TestRecord(t *testing.T) {
	t.Parallel()
	ctx := logging.WithRequestID(context.Background(), "req-0123")
	f := isolationtest.New(t, dbtest.New(t), nil)
	a := f.A
	token := ids.New()
	ip := netip.MustParseAddr("2001:db8::7")

	var project ids.UUID
	if err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		project = addProject(t, tx, a.ID)
		return Record(ctx, tx, Entry{Action: actionChange, WorkspaceID: a.ID, ActorUserID: a.UserID,
			ActorTokenID: token, SubjectID: project, Result: ResultSuccess, IP: ip, UserAgent: "Godot/4.3",
			Metadata: map[string]any{"rows": 3, "note": "moved"}})
	}); err != nil {
		t.Fatal(err)
	}
	got := entries(t, f.Admin(), "subject_id = $1", project)
	if len(got) != 1 {
		t.Fatalf("%d entries about the project, want 1", len(got))
	}
	e := got[0]
	if e.workspaceID != nullUUID(a.ID) || e.actorUserID != nullUUID(a.UserID) || e.actorTokenID != nullUUID(token) ||
		e.action != "test.change" || e.result != "success" || e.subjectKind.String != "project" ||
		e.userAgent.String != "Godot/4.3" || e.requestID.String != "req-0123" || e.ip == nil || *e.ip != ip {
		t.Errorf("the entry is %+v", e)
	}
	if want := map[string]any{"rows": float64(3), "note": "moved"}; len(e.metadata) != 2 ||
		e.metadata["rows"] != want["rows"] || e.metadata["note"] != want["note"] {
		t.Errorf("metadata = %v, want %v", e.metadata, want)
	}

	// An event of no workspace, recorded without a subject, an IP, a user
	// agent, or a request ID.
	if err := f.DB.InTx(context.Background(), database.Scope{}, func(tx pgx.Tx) error {
		return Record(context.Background(), tx, Entry{Action: ActionLogin, Result: ResultFailure})
	}); err != nil {
		t.Fatal(err)
	}
	got = entries(t, f.Admin(), "action = 'auth.login'")
	if len(got) != 1 {
		t.Fatalf("%d login entries, want 1", len(got))
	}
	e = got[0]
	if e.workspaceID.Valid || e.actorUserID.Valid || e.actorTokenID.Valid || e.subjectID.Valid ||
		e.subjectKind.Valid || e.userAgent.Valid || e.requestID.Valid || e.ip != nil || e.result != "failure" ||
		len(e.metadata) != 0 {
		t.Errorf("the login entry is %+v", e)
	}

	// What a client sends that the columns cannot hold as it is: an address
	// with a zone, and a user agent that is too long, with bytes that are not
	// UTF-8 and a NUL character.
	agent := "\xff\x00" + strings.Repeat("é", MaxUserAgentBytes)
	if err := f.DB.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		return Record(ctx, tx, Entry{Action: ActionLogin, ActorUserID: a.UserID, SubjectID: a.UserID,
			Result: ResultSuccess, IP: netip.MustParseAddr("fe80::1%eth0"), UserAgent: agent})
	}); err != nil {
		t.Fatal(err)
	}
	got = entries(t, f.Admin(), "action = 'auth.login' AND subject_id = $1", a.UserID)
	if len(got) != 1 {
		t.Fatalf("%d login entries about the user, want 1", len(got))
	}
	e = got[0]
	if want := ClientText(agent, MaxUserAgentBytes); e.userAgent.String != want || len(want) > MaxUserAgentBytes {
		t.Errorf("the user agent is %q, want %q", e.userAgent.String, want)
	}
	if e.ip == nil || *e.ip != netip.MustParseAddr("fe80::1") {
		t.Errorf("the IP is %v, want fe80::1", e.ip)
	}
}

// TestRecordInTheChangesTransaction checks that an entry commits exactly when
// the change that it records does: both commit together, in one transaction,
// and roll back together, and an entry that Record refuses, or that the
// database refuses, takes the change with it.
//
// Verifies: FR-TEN-07/AC3
func TestRecordInTheChangesTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), nil)
	a := f.A
	change := func(project ids.UUID) Entry {
		return Entry{Action: actionChange, WorkspaceID: a.ID, ActorUserID: a.UserID, SubjectID: project,
			Result: ResultSuccess}
	}
	exists := func(project ids.UUID) (bool, int) {
		t.Helper()
		var ok bool
		if err := f.Admin().QueryRow(ctx, "SELECT EXISTS (SELECT FROM project WHERE id = $1)", project).
			Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok, len(entries(t, f.Admin(), "subject_id = $1", project))
	}

	var committed ids.UUID
	if err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		committed = addProject(t, tx, a.ID)
		return Record(ctx, tx, change(committed))
	}); err != nil {
		t.Fatal(err)
	}
	if ok, n := exists(committed); !ok || n != 1 {
		t.Errorf("committed: the project exists %v, with %d entries, want true and 1", ok, n)
	}
	// Rows that one transaction added have its ID as their xmin, and the
	// time it started as now().
	var sameTx, sameTime bool
	if err := f.Admin().QueryRow(ctx, `SELECT p.xmin::text = a.xmin::text, p.created_at = a.created_at
		FROM project p, audit_entry a WHERE p.id = $1 AND a.subject_id = $1`, committed).
		Scan(&sameTx, &sameTime); err != nil {
		t.Fatal(err)
	}
	if !sameTx || !sameTime {
		t.Errorf("the entry and the project were added in the same transaction %v, at the same time %v, want both",
			sameTx, sameTime)
	}

	var rolledBack ids.UUID
	err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		rolledBack = addProject(t, tx, a.ID)
		if err := Record(ctx, tx, change(rolledBack)); err != nil {
			return err
		}
		return errRolledBack
	})
	if !errors.Is(err, errRolledBack) {
		t.Fatal(err)
	}
	if ok, n := exists(rolledBack); ok || n != 0 {
		t.Errorf("rolled back: the project exists %v, with %d entries, want neither", ok, n)
	}

	for _, c := range []struct {
		name string
		e    func(project ids.UUID) Entry
		// denied says that the database refuses the entry, rather than Record.
		denied bool
	}{
		{"a result that the event cannot have", func(p ids.UUID) Entry {
			e := change(p)
			e.Result = ResultFailure
			return e
		}, false},
		{"metadata that does not encode as JSON", func(p ids.UUID) Entry {
			e := change(p)
			e.Metadata = map[string]any{"note": func() {}}
			return e
		}, false},
		{"another workspace's entry", func(p ids.UUID) Entry {
			e := change(p)
			e.WorkspaceID = f.B.ID
			return e
		}, true},
	} {
		var project ids.UUID
		err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
			project = addProject(t, tx, a.ID)
			return Record(ctx, tx, c.e(project))
		})
		switch {
		case err == nil:
			t.Errorf("%s: Record succeeds", c.name)
		case c.denied != isDenied(err):
			t.Errorf("%s: %v, want the database to refuse it %v", c.name, err, c.denied)
		}
		if ok, n := exists(project); ok || n != 0 {
			t.Errorf("%s: the project exists %v, with %d entries, want neither", c.name, ok, n)
		}
	}
}

// TestEntriesCannotChange checks that the application login can neither
// change nor delete an entry, its own workspace's or one of no workspace, by
// UPDATE, DELETE, or TRUNCATE, which its grants refuse whatever row-level
// security would let through. TestTenancyPrivileges, in package database,
// pins the grants of storycreep_maintenance, which only the audit retention
// job is to have DELETE added to.
//
// Verifies: FR-TEN-07/AC1
func TestEntriesCannotChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), nil)
	a := f.A

	var project ids.UUID
	if err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		project = addProject(t, tx, a.ID)
		return Record(ctx, tx, Entry{Action: actionChange, WorkspaceID: a.ID, SubjectID: project,
			Result: ResultSuccess})
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.InTx(ctx, database.Scope{UserID: a.UserID}, func(tx pgx.Tx) error {
		return Record(ctx, tx, Entry{Action: ActionLogin, ActorUserID: a.UserID, SubjectID: a.UserID,
			Result: ResultSuccess})
	}); err != nil {
		t.Fatal(err)
	}
	before := entries(t, f.Admin(), "true")
	if len(before) != 2 {
		t.Fatalf("%d entries, want 2", len(before))
	}

	for _, c := range []struct {
		name  string
		scope database.Scope
		sql   string
		args  []any
	}{
		{"update an entry of the workspace", a.Scope(),
			"UPDATE audit_entry SET result = 'failure' WHERE subject_id = $1", []any{project}},
		{"delete an entry of the workspace", a.Scope(), "DELETE FROM audit_entry WHERE subject_id = $1",
			[]any{project}},
		{"update an entry of no workspace", database.Scope{UserID: a.UserID},
			"UPDATE audit_entry SET result = 'failure' WHERE subject_id = $1", []any{a.UserID}},
		{"delete an entry of no workspace", database.Scope{UserID: a.UserID},
			"DELETE FROM audit_entry WHERE subject_id = $1", []any{a.UserID}},
		{"truncate the log", a.Scope(), "TRUNCATE audit_entry", nil},
	} {
		err := f.DB.InTx(ctx, c.scope, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, c.sql, c.args...)
			return err
		})
		if !isDenied(err) {
			t.Errorf("%s: %v, want the application login's privileges to refuse it", c.name, err)
		}
	}
	if after := entries(t, f.Admin(), "true"); !slices.EqualFunc(after, before, func(x, y stored) bool {
		return x.text == y.text
	}) {
		t.Errorf("the entries changed:\n%v\nwant\n%v", after, before)
	}
}

// TestEntriesOfNoWorkspace checks how row-level security holds the entries of
// the events that belong to no workspace: a transaction without a workspace
// can add them, but read none of them, and add no entry of a workspace, and a
// transaction of a workspace can neither add nor read them, so that no
// request can file an event of its workspace outside it.
//
// Verifies: NFR-SEC-01
func TestEntriesOfNoWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), nil)
	a := f.A
	login := Entry{Action: ActionLogin, ActorUserID: a.UserID, SubjectID: a.UserID, Result: ResultSuccess}
	const count = "SELECT count(*) FROM audit_entry WHERE subject_id = $1"
	const insert = "INSERT INTO audit_entry (id, workspace_id, action, result) VALUES ($1, $2, 'auth.login', 'success')"

	var seen int
	if err := f.DB.InTx(ctx, database.Scope{UserID: a.UserID}, func(tx pgx.Tx) error {
		if err := Record(ctx, tx, login); err != nil {
			return err
		}
		return tx.QueryRow(ctx, count, a.UserID).Scan(&seen)
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Errorf("a transaction without a workspace reads %d entries of no workspace, want 0", seen)
	}
	if n := len(entries(t, f.Admin(), "subject_id = $1 AND workspace_id IS NULL", a.UserID)); n != 1 {
		t.Errorf("%d entries of no workspace, want 1", n)
	}

	err := f.DB.InTx(ctx, database.Scope{UserID: a.UserID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insert, ids.New(), a.ID)
		return err
	})
	if !isDenied(err) {
		t.Errorf("a transaction without a workspace adds an entry of a workspace: %v, want row-level security "+
			"to refuse it", err)
	}

	err = f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		return Record(ctx, tx, login)
	})
	if !isDenied(err) {
		t.Errorf("a transaction of a workspace records an event of no workspace: %v, want row-level security "+
			"to refuse it", err)
	}
	err = f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insert, ids.New(), nil)
		return err
	})
	if !isDenied(err) {
		t.Errorf("a transaction of a workspace adds an entry of no workspace: %v, want row-level security "+
			"to refuse it", err)
	}
	if err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, count, a.UserID).Scan(&seen)
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Errorf("a transaction of a workspace reads %d entries of no workspace, want 0", seen)
	}
}

// TestAttemptedEmail checks that a failed login for an address of no account
// records the address only as its keyed hash, with the key that migrating
// generated, of the address in lower case, so that the hashes of one address
// match whatever its case, and that no entry of a login holds an account's
// email address or display name, which entries name by ID.
func TestAttemptedEmail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), nil)
	admin := f.Admin()
	var key []byte
	if err := admin.QueryRow(ctx, "SELECT key FROM instance_key WHERE name = 'audit_email'").Scan(&key); err != nil {
		t.Fatal(err)
	}
	user := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, $3)`, user,
		"Known.Person@Example.org", "Known Person"); err != nil {
		t.Fatal(err)
	}

	for _, e := range []Entry{
		{Action: ActionLogin, Result: ResultFailure, AttemptedEmail: "Nobody@Example.com"},
		{Action: ActionLogin, Result: ResultFailure, AttemptedEmail: "NOBODY@example.COM"},
		{Action: ActionLogin, Result: ResultFailure, AttemptedEmail: "somebody@example.com"},
		// Text that a text column cannot hold, as a client can send.
		{Action: ActionLogin, Result: ResultFailure, AttemptedEmail: "bad\x00\xffbytes@example.com"},
		{Action: ActionLogin, Result: ResultFailure, SubjectID: user},
		{Action: ActionLogin, Result: ResultSuccess, ActorUserID: user, SubjectID: user},
	} {
		if err := f.DB.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			return Record(ctx, tx, e)
		}); err != nil {
			t.Fatalf("record %+v: %v", e, err)
		}
	}
	got := entries(t, admin, "action = 'auth.login'")
	if len(got) != 6 {
		t.Fatalf("%d login entries, want 6", len(got))
	}
	hash := func(email string) string {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(email))
		return hex.EncodeToString(mac.Sum(nil))
	}
	for i, want := range []string{hash("nobody@example.com"), hash("nobody@example.com"),
		hash("somebody@example.com"), hash("bad�bytes@example.com")} {
		if m := got[i].metadata; len(m) != 1 || m[emailHashKey] != want {
			t.Errorf("entry %d: metadata %v, want %s %s", i, m, emailHashKey, want)
		}
	}
	for i, e := range got {
		if s := strings.ToLower(e.text); strings.Contains(s, "body") || strings.Contains(s, "known") ||
			strings.Contains(s, "example") {
			t.Errorf("entry %d holds an email address or a display name: %s", i, e.text)
		}
	}
	if got[4].metadata[emailHashKey] != nil || got[4].subjectID != nullUUID(user) {
		t.Errorf("the failed login for an account is %+v, want its subject and no hash", got[4])
	}
}

// TestInstanceKey checks the key of the email hash: 32 bytes, which the
// application may read but neither change, add to, nor delete, and which
// migrating generates anew for each database, so that the hashes of one
// instance say nothing about another's.
func TestInstanceKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	db := d.Open(t)
	if err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT octet_length(key) FROM instance_key WHERE name = 'audit_email'").
			Scan(&n); err != nil {
			return err
		}
		if n != 32 {
			t.Errorf("the key has %d bytes, want 32", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"UPDATE instance_key SET key = sha256(key)",
		"DELETE FROM instance_key",
		"INSERT INTO instance_key (name, key) VALUES ('other', sha256('x'))",
		"TRUNCATE instance_key",
	} {
		err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql)
			return err
		})
		if !isDenied(err) {
			t.Errorf("%s: %v, want the application login's privileges to refuse it", sql, err)
		}
	}

	var keys [][]byte
	for range 2 {
		e := dbtest.Empty(t)
		if _, err := database.Migrate(ctx, e.AdminURL, e.AppURL, database.MigrateOptions{}); err != nil {
			t.Fatal(err)
		}
		var key []byte
		if err := e.Admin(t).QueryRow(ctx, "SELECT key FROM instance_key WHERE name = 'audit_email'").
			Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if string(keys[0]) == string(keys[1]) {
		t.Errorf("two databases have the same key %x", keys[0])
	}
}

// TestCheck checks that Record refuses each entry that its event's
// registration does not allow, before it adds anything, and allows the
// others.
func TestCheck(t *testing.T) {
	t.Parallel()
	w, u := ids.New(), ids.New()
	for _, c := range []struct {
		e    Entry
		want string
	}{
		{Entry{Action: actionChange, WorkspaceID: w, SubjectID: u, Result: ResultSuccess,
			Metadata: map[string]any{"rows": 1}}, ""},
		{Entry{Action: actionChange, WorkspaceID: w, Result: ResultDenied}, ""},
		{Entry{Action: ActionLogin, ActorUserID: u, SubjectID: u, Result: ResultSuccess}, ""},
		{Entry{Action: ActionLogin, Result: ResultFailure, AttemptedEmail: "a@example.com"}, ""},
		{Entry{Action: "test.unknown", WorkspaceID: w, Result: ResultSuccess},
			`the action "test.unknown" is not registered`},
		{Entry{Result: ResultSuccess}, `the action "" is not registered`},
		{Entry{Action: actionChange, Result: ResultSuccess}, "test.change belongs to a workspace, but the entry has none"},
		{Entry{Action: ActionLogin, WorkspaceID: w, Result: ResultSuccess},
			"auth.login belongs to no workspace, but the entry has one"},
		{Entry{Action: ActionLogin, Result: ResultDenied}, `auth.login cannot have the result "denied"`},
		{Entry{Action: ActionLogin}, `auth.login cannot have the result ""`},
		{Entry{Action: actionChange, WorkspaceID: w, Result: ResultSuccess, Metadata: map[string]any{"email": "x"}},
			`test.change has no metadata "email"`},
		{Entry{Action: ActionLogin, Result: ResultFailure, Metadata: map[string]any{emailHashKey: "00"}},
			`auth.login has no metadata "email_hmac"`},
		{Entry{Action: actionChange, WorkspaceID: w, Result: ResultSuccess, AttemptedEmail: "a@example.com"},
			"test.change records no attempted email address"},
		{Entry{Action: ActionLogin, Result: ResultSuccess, AttemptedEmail: "a@example.com"},
			"auth.login records an attempted email address only when it fails"},
		{Entry{Action: ActionLogin, Result: ResultFailure, SubjectID: u, AttemptedEmail: "a@example.com"},
			"auth.login names the account that the address belongs to"},
		{Entry{Action: actionBare, WorkspaceID: w, SubjectID: u, Result: ResultSuccess},
			"test.bare has no subject, but the entry has one"},
	} {
		_, err := check(c.e)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%+v: %v", c.e, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%+v: %v, want %q", c.e, err, c.want)
		}
	}
}

// TestEvents checks the registry: each action is registered once, is a
// lowercase noun and verb, as auth.login, and can have known results, and
// each event's metadata keys are its own, unlike emailHashKey, which only
// Record sets.
func TestEvents(t *testing.T) {
	name := regexp.MustCompile(`^[a-z]+(_[a-z]+)*\.[a-z]+(_[a-z]+)*$`)
	known := []Result{ResultSuccess, ResultFailure, ResultDenied}
	var seen []Action
	for _, s := range events {
		if slices.Contains(seen, s.action) {
			t.Errorf("%s is registered twice", s.action)
		}
		seen = append(seen, s.action)
		if !name.MatchString(string(s.action)) {
			t.Errorf("%s is not a lowercase noun and verb, as auth.login", s.action)
		}
		if len(s.results) == 0 {
			t.Errorf("%s can have no result", s.action)
		}
		for _, r := range s.results {
			if !slices.Contains(known, r) {
				t.Errorf("%s can have the unknown result %q", s.action, r)
			}
		}
		if s.attemptedEmail && (!slices.Contains(s.results, ResultFailure) || s.subject != "user") {
			t.Errorf("%s records an attempted email address, but cannot fail, or is not about an account", s.action)
		}
		for i, k := range s.metadata {
			if k == emailHashKey || slices.Contains(s.metadata[:i], k) {
				t.Errorf("%s has the metadata key %q twice, or the key that Record sets", s.action, k)
			}
		}
	}
}

// TestClientText checks that ClientText keeps only what a text column can
// hold, cut at a character's boundary.
func TestClientText(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in    string
		limit int
		want  string
	}{
		{"Godot/4.3", 0, "Godot/4.3"},
		{"a\x00b", 0, "ab"},
		{"a\xff\xfeb", 0, "a�b"},
		{"abcdef", 4, "abcd"},
		// é is two bytes, which the limit would split.
		{"abcé", 4, "abc"},
		{"abcé", 5, "abcé"},
		{"", 4, ""},
	} {
		if got := ClientText(c.in, c.limit); got != c.want {
			t.Errorf("ClientText(%q, %d) = %q, want %q", c.in, c.limit, got, c.want)
		}
	}
}
