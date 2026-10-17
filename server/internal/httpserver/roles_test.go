package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// memberFrom returns the member that a change of a role answered with,
// which must be 200.
func memberFrom(t *testing.T, res response) memberBody {
	t.Helper()
	var m memberBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &m) != nil {
		t.Fatalf("change the role: %d %s, want 200 and a member", res.status, res.body)
	}
	return m
}

// TestRoleChangeOwnerRequired checks that an admin who makes a member owner,
// or changes an owner's role, is answered with 403 owner_required, and
// changes nothing, while an admin changes the other roles, and an owner does
// all of it.
//
// Verifies: FR-TEN-03/AC4
func TestRoleChangeOwnerRequired(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	admin := newNamedAccount(t, s.main, "ada@example.com", "Ada")
	join(t, d, admin, ws, "admin")
	editor := newNamedAccount(t, s.main, "eda@example.com", "Eda")
	join(t, d, editor, ws, "editor")
	members := membersOf(t, s.main, owner, ws)
	owners, editors := membershipOf(t, members, owner), membershipOf(t, members, editor)

	wantCode(t, "an admin makes an editor owner", changeRole(t, s.main, admin, ws, editors, "owner"),
		http.StatusForbidden, api.CodeOwnerRequired)
	wantCode(t, "an admin demotes an owner", changeRole(t, s.main, admin, ws, owners, "admin"),
		http.StatusForbidden, api.CodeOwnerRequired)
	wantCode(t, "an admin makes themselves owner", changeRole(t, s.main, admin, ws, membershipOf(t, members, admin),
		"owner"), http.StatusForbidden, api.CodeOwnerRequired)
	for _, m := range membersOf(t, s.main, owner, ws) {
		if want := map[string]string{owners: "owner", editors: "editor"}[m.ID]; want != "" && m.Role != want {
			t.Errorf("a refused change made %s %s", m.DisplayName, m.Role)
		}
	}
	if m := memberFrom(t, changeRole(t, s.main, admin, ws, editors, "admin")); m.Role != "admin" ||
		m.Email != "eda@example.com" || m.ID != editors || m.UserID != editor.body.UserID {
		t.Errorf("an admin makes an editor admin: %+v", m)
	}
	if m := memberFrom(t, changeRole(t, s.main, owner, ws, editors, "owner")); m.Role != "owner" {
		t.Errorf("an owner makes an admin owner: %+v", m)
	}
	if m := memberFrom(t, changeRole(t, s.main, owner, ws, editors, "viewer")); m.Role != "viewer" {
		t.Errorf("an owner demotes an owner: %+v", m)
	}
}

// TestDemoteLastOwner checks that a workspace's last owner whose account is
// active cannot be demoted, with 409 last_owner, even while an owner whose
// account's deletion is pending remains, and can be once another member is
// owner.
//
// Verifies: FR-TEN-02/AC2
func TestDemoteLastOwner(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	leaving := newNamedAccount(t, s.main, "lee@example.com", "Lee")
	join(t, d, leaving, ws, "owner")
	if _, err := d.Admin(t).Exec(t.Context(), `UPDATE "user" SET status = 'pending_deletion' WHERE id = $1`,
		decode(t, ids.User, leaving.body.UserID)); err != nil {
		t.Fatal(err)
	}
	other := newNamedAccount(t, s.main, "oli@example.com", "Oli")
	join(t, d, other, ws, "editor")
	members := membersOf(t, s.main, owner, ws)
	owners := membershipOf(t, members, owner)
	for _, r := range []string{"admin", "viewer", "guest"} {
		wantCode(t, "the last owner makes themselves "+r, changeRole(t, s.main, owner, ws, owners, r),
			http.StatusConflict, api.CodeLastOwner)
	}
	if res := changeRole(t, s.main, owner, ws, owners, "owner"); res.status != http.StatusOK {
		t.Errorf("the last owner stays owner: %d %s", res.status, res.body)
	}
	memberFrom(t, changeRole(t, s.main, owner, ws, membershipOf(t, members, other), "owner"))
	if m := memberFrom(t, changeRole(t, s.main, owner, ws, owners, "admin")); m.Role != "admin" {
		t.Errorf("an owner of two makes themselves admin: %+v", m)
	}
}

// TestRoleChangeRevokesInvites checks that a demotion revokes the open
// invitations that the member made, and those whose link the member was
// given by sending them anew, whose role the new role may not give: every
// one, for a role that does not manage members, and those as owner, for
// admin. Their links are refused, each revocation is recorded with the
// change's actor and membership, and the others stay open.
//
// Verifies: FR-TEN-02/AC4
func TestRoleChangeRevokesInvites(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	ada := newNamedAccount(t, s.main, "ada@example.com", "Ada")
	join(t, d, ada, ws, "admin")
	bo := newNamedAccount(t, s.main, "bo@example.com", "Bo")
	join(t, d, bo, ws, "owner")

	// Ada's link invitation and invitation to an address, whose links she
	// was given, and the owner's invitation, whose new link she takes by
	// sending it anew.
	adasLink := linkToken(t, createInvite(t, s.main, ada, ws, "", "editor").URL)
	adasAddress := linkToken(t, createInvite(t, s.main, ada, ws, "jo@example.com", "viewer").URL)
	owners := createInvite(t, s.main, owner, ws, "", "commenter")
	res := call(s.main, http.MethodPost, invitesPath(ws)+"/"+owners.ID+"/resend", nil, ada.header(true))
	var resent inviteBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &resent) != nil {
		t.Fatalf("ada sends the owner's invitation anew: %d %s", res.status, res.body)
	}
	// Bo's invitations as owner and as editor, and the owner's own.
	bosOwner := linkToken(t, createInvite(t, s.main, bo, ws, "", "owner").URL)
	bosEditor := createInvite(t, s.main, bo, ws, "", "editor")
	kept := createInvite(t, s.main, owner, ws, "", "admin")

	members := membersOf(t, s.main, owner, ws)
	adas, bos := membershipOf(t, members, ada), membershipOf(t, members, bo)
	memberFrom(t, changeRole(t, s.main, owner, ws, adas, "editor"))
	memberFrom(t, changeRole(t, s.main, owner, ws, bos, "admin"))

	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	for what, token := range map[string]string{"the link invitation that ada made": adasLink,
		"the invitation to an address that ada made": adasAddress, "the invitation that ada sent anew": linkToken(t,
			resent.URL), "the invitation as owner that bo made": bosOwner} {
		wantCode(t, "accept "+what, acceptInvite(t, s.main, jo, token), http.StatusGone, api.CodeInviteRevoked)
	}
	open := map[string]bool{}
	for _, inv := range invitesOf(t, s.main, owner, ws) {
		open[inv.ID] = true
	}
	if len(open) != 2 || !open[bosEditor.ID] || !open[kept.ID] {
		t.Errorf("the open invitations are %v, want bo's as editor and the owner's", open)
	}
	for name, c := range map[string]struct {
		membership string
		want       int
	}{"ada": {adas, 3}, "bo": {bos, 1}} {
		n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'invite.revoke'
			AND actor_user_id = $1::uuid AND metadata->>'membership_id' = $2`,
			decode(t, ids.User, owner.body.UserID).String(), decode(t, ids.Membership, c.membership).String())
		if n != c.want {
			t.Errorf("the demotion of %s revoked %d invitations, want %d", name, n, c.want)
		}
	}
}

// TestRoleChangeAuditEvents checks that a change of a member's role is
// recorded with its actor, the membership, and the roles before and after,
// and that a change to the role that the membership has already records
// nothing.
//
// Verifies: FR-TEN-07/AC2
func TestRoleChangeAuditEvents(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	join(t, d, hana, ws, "editor")
	hanas := membershipOf(t, membersOf(t, s.main, owner, ws), hana)
	memberFrom(t, changeRole(t, s.main, owner, ws, hanas, "commenter"))
	memberFrom(t, changeRole(t, s.main, owner, ws, hanas, "commenter"))
	n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'membership.role_change'
		AND actor_user_id = $1::uuid AND workspace_id = $2::uuid AND subject_id = $3::uuid AND result = 'success'
		AND metadata = '{"role": "commenter", "previous_role": "editor"}'::jsonb AND ip IS NOT NULL`,
		decode(t, ids.User, owner.body.UserID).String(), decode(t, ids.Workspace, ws).String(),
		decode(t, ids.Membership, hanas).String())
	if n != 1 {
		t.Errorf("%d entries of the role change, want 1", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'membership.role_change'`); n != 1 {
		t.Errorf("%d role changes recorded, want 1", n)
	}
}
