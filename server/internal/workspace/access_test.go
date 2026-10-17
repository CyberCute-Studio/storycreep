package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// addWorkspace adds a workspace with the slug, as the superuser, and returns
// its ID.
func addWorkspace(t *testing.T, d *dbtest.Database, slug string) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := d.Admin(t).Exec(context.Background(),
		`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, $2, $2, 'unlimited')`, id, slug); err != nil {
		t.Fatal(err)
	}
	return id
}

// addMember makes the user a member of the workspace with the role, as the
// superuser.
func addMember(t *testing.T, d *dbtest.Database, workspaceID, userID ids.UUID, r role.Role) {
	t.Helper()
	if _, err := d.Admin(t).Exec(context.Background(),
		`INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, $4)`, ids.New(), workspaceID,
		userID, string(r)); err != nil {
		t.Fatal(err)
	}
}

// accessState returns the InstanceAdminAccess capability that the instance
// recorded, as "true", "false", or "null".
func accessState(t *testing.T, d *dbtest.Database) string {
	t.Helper()
	var state string
	if err := d.Admin(t).QueryRow(context.Background(),
		`SELECT coalesce(instance_admin_access::text, 'null') FROM instance`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// TestRevokeAccessInvites checks that the end of an account's instance
// admin access revokes, in every workspace, the open invitations that it
// made or was given the link of whose roles its membership may not give:
// all of them where it is not a member or is a guest or an editor, those as
// owner where it is an admin, and none where it is an owner. Each is
// recorded without an actor, naming the account, the other invitations stay
// open, and a second run revokes nothing. Its workspaces are more than one
// page of the listing, so that every page is gone through.
//
// Verifies: FR-TEN-02/AC4
func TestRevokeAccessInvites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newService(t, role.Policy{})
	ana, other := addUser(t, d, "Ana"), addUser(t, d, "Other")
	// Two pages of workspaces whose IDs sort before those of the workspaces
	// with invitations, which ids.New makes from the time, so that those are
	// on the listing's third page.
	if _, err := d.Admin(t).Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key)
		SELECT ('00000000-0000-7000-8000-' || lpad(n::text, 12, '0'))::uuid, 'Filler', 'filler-' || n, 'unlimited'
		FROM generate_series(1, $1) n`, 2*workspacePage); err != nil {
		t.Fatal(err)
	}
	type invitation struct {
		id      ids.UUID
		revoked bool
	}
	var invitations []invitation
	for _, c := range []struct {
		member  role.Role
		revoked map[role.Role]bool
	}{
		{role.None, map[role.Role]bool{role.Owner: true, role.Viewer: true}},
		{role.Guest, map[role.Role]bool{role.Admin: true}},
		{role.Editor, map[role.Role]bool{role.Commenter: true}},
		{role.Admin, map[role.Role]bool{role.Owner: true, role.Admin: false, role.Guest: false}},
		{role.Owner, map[role.Role]bool{role.Owner: false}},
	} {
		w := addWorkspace(t, d, "harbour-"+string(c.member))
		if c.member != role.None {
			addMember(t, d, w, ana, c.member)
		}
		for r, revoked := range c.revoked {
			// Half made by ana, half made by another, whose link ana took.
			made := addInvite(t, d, w, ana, other, r)
			held := addInvite(t, d, w, other, ana, r)
			invitations = append(invitations, invitation{made, revoked}, invitation{held, revoked})
		}
		// Another's invitation as owner stays open everywhere.
		invitations = append(invitations, invitation{addInvite(t, d, w, other, other, role.Owner), false})
	}

	n, err := s.RevokeAccessInvites(ctx, []ids.UUID{ana})
	want := 0
	for _, inv := range invitations {
		if inv.revoked {
			want++
		}
		if got := revoked(t, d, inv.id); got != inv.revoked {
			t.Errorf("the invitation %s revoked %v, want %v", inv.id, got, inv.revoked)
		}
	}
	if err != nil || n != want {
		t.Errorf("RevokeAccessInvites = %d, %v, want %d", n, err, want)
	}
	if got := count(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'invite.revoke'
		AND actor_user_id IS NULL AND metadata->>'user_id' = $1`, ana.String()); got != want {
		t.Errorf("%d revocations recorded, want %d", got, want)
	}
	if n, err := s.RevokeAccessInvites(ctx, []ids.UUID{ana}); err != nil || n != 0 {
		t.Errorf("a second RevokeAccessInvites = %d, %v, want 0", n, err)
	}
}

// TestRecordAccess checks that a server that starts with InstanceAdminAccess
// on records it and revokes nothing, that the first to start with it off
// after one with it on, or after none recorded it, revokes the instance
// admins' invitations that their memberships may not give, and records that
// it is off, and that the next start with it off revokes nothing.
//
// Verifies: FR-TEN-02/AC4
func TestRecordAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	on, d := newService(t, role.Policy{InstanceAdminAccess: true})
	off := New(d.Open(t), Options{PlanKey: "unlimited"})
	admin, other := addUser(t, d, "Admin"), addUser(t, d, "Other")
	if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET is_instance_admin = true WHERE id = $1`, admin); err != nil {
		t.Fatal(err)
	}
	w := addWorkspace(t, d, "harbour")
	if got := accessState(t, d); got != "null" {
		t.Fatalf("a new instance recorded %s", got)
	}

	first := addInvite(t, d, w, admin, admin, role.Owner)
	if n, err := on.RecordAccess(ctx); err != nil || n != 0 || accessState(t, d) != "true" || revoked(t, d, first) {
		t.Errorf("a start with access on = %d, %v, recorded %s, revoked %v", n, err, accessState(t, d),
			revoked(t, d, first))
	}
	others := addInvite(t, d, w, other, other, role.Owner)
	if n, err := off.RecordAccess(ctx); err != nil || n != 1 || accessState(t, d) != "false" || !revoked(t, d, first) ||
		revoked(t, d, others) {
		t.Errorf("a start with access off after one with it on = %d, %v, recorded %s, revoked %v and %v", n, err,
			accessState(t, d), revoked(t, d, first), revoked(t, d, others))
	}
	second := addInvite(t, d, w, admin, admin, role.Owner)
	if n, err := off.RecordAccess(ctx); err != nil || n != 0 || revoked(t, d, second) {
		t.Errorf("a second start with access off = %d, %v, revoked %v", n, err, revoked(t, d, second))
	}
	if _, err := d.Admin(t).Exec(ctx, `UPDATE instance SET instance_admin_access = NULL`); err != nil {
		t.Fatal(err)
	}
	if n, err := off.RecordAccess(ctx); err != nil || n != 1 || accessState(t, d) != "false" || !revoked(t, d, second) {
		t.Errorf("a start with access off when none was recorded = %d, %v, recorded %s, revoked %v", n, err,
			accessState(t, d), revoked(t, d, second))
	}
}

// TestRecordAccessRetries checks that a start with InstanceAdminAccess off
// whose revocation fails leaves the capability that the instance recorded as
// it was, so that the next start revokes the invitations that it left open.
//
// Verifies: FR-TEN-02/AC4
func TestRecordAccessRetries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newService(t, role.Policy{})
	admin := addUser(t, d, "Admin")
	if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET is_instance_admin = true WHERE id = $1`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin(t).Exec(ctx, `UPDATE instance SET instance_admin_access = true`); err != nil {
		t.Fatal(err)
	}
	inv := addInvite(t, d, addWorkspace(t, d, "harbour"), admin, admin, role.Owner)
	// A trigger, which the superuser adds, makes every revocation fail.
	if _, err := d.Admin(t).Exec(ctx, `
		CREATE FUNCTION fail_revocation() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'the revocation fails';
		END
		$$;
		CREATE TRIGGER fail_revocation BEFORE UPDATE ON invite
			FOR EACH ROW EXECUTE FUNCTION fail_revocation()`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RecordAccess(ctx); err == nil || accessState(t, d) != "true" || revoked(t, d, inv) {
		t.Errorf("a start whose revocation fails = %d, %v, recorded %s, revoked %v; want an error, true, and false",
			n, err, accessState(t, d), revoked(t, d, inv))
	}
	if _, err := d.Admin(t).Exec(ctx, `DROP TRIGGER fail_revocation ON invite`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RecordAccess(ctx); err != nil || n != 1 || accessState(t, d) != "false" || !revoked(t, d, inv) {
		t.Errorf("the next start = %d, %v, recorded %s, revoked %v", n, err, accessState(t, d), revoked(t, d, inv))
	}
}

// TestAccessWaitsForRevocation checks that an operation that an instance
// admin's role authorises, while InstanceAdminAccess is on, locks the
// account, so that it waits for a change of the account that makes it no
// longer an instance admin, and is then refused, rather than act on the role
// that the account had: an invitation that it made then would escape the
// revocation that follows the change. It tries a change of the workspace's
// settings, and the authorisation that making or resending an invitation
// takes (package invite).
func TestAccessWaitsForRevocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d := newService(t, role.Policy{InstanceAdminAccess: true})
	owner := addUser(t, d, "Owner")
	w, err := s.Create(ctx, owner, Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	for _, c := range []struct {
		name string
		// op is the operation, which the instance admin admin asks for.
		op func(admin ids.UUID) error
	}{
		{"the instance admin's change of the settings", func(admin ids.UUID) error {
			_, _, err := s.Update(ctx, admin, w.ID, Patch{Name: &name})
			return err
		}},
		{"the instance admin's authorisation of an invitation", func(admin ids.UUID) error {
			return s.db.InTx(ctx, database.Scope{WorkspaceID: w.ID, UserID: admin}, func(tx pgx.Tx) error {
				_, err := s.Authorize(ctx, tx, admin, w.ID)
				return err
			})
		}},
	} {
		admin := addUser(t, d, "Admin")
		if _, err := d.Admin(t).Exec(ctx, `UPDATE "user" SET is_instance_admin = true WHERE id = $1`,
			admin); err != nil {
			t.Fatal(err)
		}
		tx, err := d.Admin(t).Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `UPDATE "user" SET is_instance_admin = false WHERE id = $1`, admin); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- c.op(admin) }()
		dbtest.AwaitWaiter(t, tx, done, c.name)
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, ErrNotFound) {
			t.Errorf("%s, once it is no longer one: %v, want ErrNotFound", c.name, err)
		}
	}
	if n := count(t, d, `SELECT count(*) FROM workspace WHERE name = 'Renamed'`); n != 0 {
		t.Errorf("the former instance admin renamed the workspace")
	}
}
