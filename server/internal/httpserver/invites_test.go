package httpserver

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/email/emailtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

const (
	lookupInvitePath   = "/api/v1/invites/lookup"
	acceptInvitePath   = "/api/v1/invites/accept"
	registerInvitePath = "/api/v1/invites/register"
)

// invitesPath and membersPath return the paths of the invitations and the
// members of the workspace ws.
func invitesPath(ws string) string { return workspacesPath + "/" + ws + "/invites" }
func membersPath(ws string) string { return workspacesPath + "/" + ws + "/members" }

// inviteEnv returns the configuration of an instance under the signup
// policy, with extra, behind a proxy at the address of httptest's requests,
// which call names another client through for each request, so that the
// rate limits of one address do not count all of a test's requests.
func inviteEnv(policy string, extra map[string]string) map[string]string {
	env := map[string]string{"STORYCREEP_SIGNUP": policy, "STORYCREEP_TRUSTED_PROXIES": "192.0.2.1/32"}
	maps.Copy(env, extra)
	return env
}

// call sends a request with a JSON body, as a client of its own (newClient),
// with header.
func call(h http.Handler, method, path string, body []byte, header http.Header) response {
	hdr := http.Header{}
	maps.Copy(hdr, header)
	maps.Copy(hdr, newClient())
	return send(h, method, path, body, "", hdr)
}

// inviteServer returns a ready server with the configuration of
// inviteEnv(policy, extra), whose first-run setup made admin@example.com the
// owner of its first workspace, signed in, and the workspace's ID.
func inviteServer(t *testing.T, policy string, extra map[string]string) (*server, *dbtest.Database, signedIn, string) {
	t.Helper()
	s, d := pendingServer(t, inviteEnv(policy, extra))
	setup := completeSetup(t, s, "admin@example.com", map[string]any{"signup_policy": policy})
	return s, d, signIn(t, s.main, "admin@example.com"), setup.Workspace.ID
}

// createInvite invites the address email, or whoever holds the link if it is
// "", to the workspace ws with role, as si, and returns the invitation.
func createInvite(t *testing.T, h http.Handler, si signedIn, ws, email, role string) inviteBody {
	t.Helper()
	body := map[string]string{"role": role}
	if email != "" {
		body["email"] = email
	}
	res := call(h, http.MethodPost, invitesPath(ws), jsonBody(t, body), si.header(true))
	var inv inviteBody
	if res.status != http.StatusCreated || json.Unmarshal(res.body, &inv) != nil {
		t.Fatalf("invite %q as %s: %d %s, want 201", email, role, res.status, res.body)
	}
	return inv
}

// linkToken returns the token of an invitation's link.
func linkToken(t *testing.T, url string) string {
	t.Helper()
	token, ok := strings.CutPrefix(url, "http://localhost:8080/app/invite#token=")
	if !ok || token == "" {
		t.Fatalf("the invitation's link is %q", url)
	}
	return token
}

// inviteToken returns the JSON body of a request that names an invitation by
// its link's token.
func inviteToken(t *testing.T, token string) []byte {
	t.Helper()
	return jsonBody(t, map[string]string{"token": token})
}

// acceptInvite accepts the invitation of token as si.
func acceptInvite(t *testing.T, h http.Handler, si signedIn, token string) response {
	t.Helper()
	return call(h, http.MethodPost, acceptInvitePath, inviteToken(t, token), si.header(true))
}

// accepted returns the membership that accepting an invitation answered
// with, which must be 200.
func accepted(t *testing.T, res response) membershipBody {
	t.Helper()
	var m membershipBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &m) != nil {
		t.Fatalf("accept: %d %s, want 200 and a membership", res.status, res.body)
	}
	return m
}

// lookupInvite reads the invitation of token.
func lookupInvite(t *testing.T, h http.Handler, token string) response {
	t.Helper()
	return call(h, http.MethodPost, lookupInvitePath, inviteToken(t, token), nil)
}

// registerWithInvite registers an account with the invitation of token, with
// the address email, if it is not "", the display name Hana, and
// testPassword.
func registerWithInvite(t *testing.T, h http.Handler, token, email string) response {
	t.Helper()
	body := map[string]string{"token": token, "display_name": "Hana", "password": testPassword}
	if email != "" {
		body["email"] = email
	}
	return call(h, http.MethodPost, registerInvitePath, jsonBody(t, body), nil)
}

// membersOf returns the first page of the members of the workspace ws, as si
// sees them.
func membersOf(t *testing.T, h http.Handler, si signedIn, ws string) []memberBody {
	t.Helper()
	res := call(h, http.MethodGet, membersPath(ws), nil, si.header(false))
	var page struct {
		Items []memberBody `json:"items"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
		t.Fatalf("list the members: %d %s", res.status, res.body)
	}
	return page.Items
}

// membershipOf returns the ID of the membership of si's account in the
// members, or fails.
func membershipOf(t *testing.T, members []memberBody, si signedIn) string {
	t.Helper()
	for _, m := range members {
		if m.UserID == si.body.UserID {
			return m.ID
		}
	}
	t.Fatalf("%s is not among the members %+v", si.body.UserID, members)
	return ""
}

// invitesOf returns the first page of the open invitations of the workspace
// ws, as si sees them.
func invitesOf(t *testing.T, h http.Handler, si signedIn, ws string) []inviteBody {
	t.Helper()
	res := call(h, http.MethodGet, invitesPath(ws), nil, si.header(false))
	var page struct {
		Items []inviteBody `json:"items"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
		t.Fatalf("list the invitations: %d %s", res.status, res.body)
	}
	return page.Items
}

// wantCode fails unless res has status and the error code.
func wantCode(t *testing.T, what string, res response, status int, code api.Code) {
	t.Helper()
	if res.status != status || res.code() != code {
		t.Errorf("%s: %d %s, want %d %s", what, res.status, res.body, status, code)
	}
}

// TestInviteWithoutEmail checks that on an instance that sends no email, an
// invitation to an address gives its link, as text, to the admin who makes
// it, through which the invited account accepts it and joins the workspace,
// and that no email job is enqueued on the way. A link invitation gives its
// link in the same way, and any account accepts it.
//
// Verifies: FR-AUTH-03/AC1
func TestInviteWithoutEmail(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "open", nil)
	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	inv := createInvite(t, s.main, admin, ws, "HANA@example.com", "editor")
	if inv.Kind != "email" || inv.Email != "HANA@example.com" || inv.Role != "editor" || inv.Status != "pending" ||
		inv.SentAt != nil || inv.CreatedBy != admin.body.UserID || !strings.HasPrefix(inv.ID, "inv_") ||
		time.Until(inv.ExpiresAt) < 13*24*time.Hour {
		t.Errorf("the invitation is %+v", inv)
	}
	token := linkToken(t, inv.URL)

	res := lookupInvite(t, s.main, token)
	var p invitePreviewBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &p) != nil || p.WorkspaceName != "Harbour Studio" ||
		p.Role != "editor" || p.Kind != "email" || p.Email != "HANA@example.com" {
		t.Errorf("look the invitation up: %d %s", res.status, res.body)
	}
	m := accepted(t, acceptInvite(t, s.main, hana, token))
	if m.ID != ws || m.Role != "editor" || m.Name != "Harbour Studio" || !strings.HasPrefix(m.MembershipID, "mem_") {
		t.Errorf("the membership is %+v", m)
	}
	// Accepting it again answers as the first time.
	if again := accepted(t, acceptInvite(t, s.main, hana, token)); again != m {
		t.Errorf("accepting again answers %+v, want %+v", again, m)
	}
	wantCode(t, "look an accepted invitation up", lookupInvite(t, s.main, token), http.StatusGone,
		api.CodeInviteAccepted)
	if got := invitesOf(t, s.main, admin, ws); len(got) != 0 {
		t.Errorf("the accepted invitation is still open: %+v", got)
	}

	link := createInvite(t, s.main, admin, ws, "", "viewer")
	if link.Kind != "link" || link.Email != "" {
		t.Errorf("the link invitation is %+v", link)
	}
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	if m := accepted(t, acceptInvite(t, s.main, jo, linkToken(t, link.URL))); m.Role != "viewer" {
		t.Errorf("the link invitation gave %+v", m)
	}
	if n := countRows(t, d, "SELECT count(*) FROM river_job WHERE kind IN ('email_invite', 'email_link')"); n != 0 {
		t.Errorf("%d email jobs were enqueued on an instance that sends no email", n)
	}
	if n := countRows(t, d, "SELECT count(*) FROM invite WHERE token_hash IS NULL OR sent_at IS NOT NULL"); n != 0 {
		t.Errorf("%d invitations wait for an email", n)
	}
}

// TestInviteExpiredOrRevoked checks that an invitation that has expired, or
// been revoked, is refused with 410, whether it is read, accepted, or
// registered with, and that sending an expired one anew gives it a new link
// and 14 days more, while its old link stops working.
//
// Verifies: FR-TEN-02/AC1
func TestInviteExpiredOrRevoked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d, admin, ws := inviteServer(t, "open", nil)
	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	inv := createInvite(t, s.main, admin, ws, "hana@example.com", "editor")
	old := linkToken(t, inv.URL)
	if _, err := d.Admin(t).Exec(ctx, "UPDATE invite SET expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "read an expired invitation", lookupInvite(t, s.main, old), http.StatusGone, api.CodeInviteExpired)
	wantCode(t, "accept an expired invitation", acceptInvite(t, s.main, hana, old), http.StatusGone,
		api.CodeInviteExpired)
	wantCode(t, "register with an expired invitation", registerWithInvite(t, s.main, old, ""), http.StatusGone,
		api.CodeInviteExpired)
	if got := invitesOf(t, s.main, admin, ws); len(got) != 1 || got[0].Status != "expired" || got[0].URL != "" {
		t.Errorf("the open invitations are %+v, want the expired one, without its link", got)
	}

	res := call(s.main, http.MethodPost, invitesPath(ws)+"/"+inv.ID+"/resend", nil, admin.header(true))
	var renewed inviteBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &renewed) != nil || renewed.Status != "pending" ||
		!renewed.ExpiresAt.After(inv.ExpiresAt.Add(-time.Minute)) {
		t.Fatalf("send it anew: %d %s", res.status, res.body)
	}
	token := linkToken(t, renewed.URL)
	wantCode(t, "use the replaced link", lookupInvite(t, s.main, old), http.StatusBadRequest, api.CodeTokenInvalid)
	if res := lookupInvite(t, s.main, token); res.status != http.StatusOK {
		t.Errorf("read the renewed invitation: %d %s", res.status, res.body)
	}

	revoke := func() response {
		return call(s.main, http.MethodDelete, invitesPath(ws)+"/"+inv.ID, nil, admin.header(true))
	}
	if res := revoke(); res.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	if res := revoke(); res.status != http.StatusNoContent {
		t.Errorf("revoke again: %d %s", res.status, res.body)
	}
	wantCode(t, "read a revoked invitation", lookupInvite(t, s.main, token), http.StatusGone, api.CodeInviteRevoked)
	wantCode(t, "accept a revoked invitation", acceptInvite(t, s.main, hana, token), http.StatusGone,
		api.CodeInviteRevoked)
	wantCode(t, "register with a revoked invitation", registerWithInvite(t, s.main, token, ""), http.StatusGone,
		api.CodeInviteRevoked)
	wantCode(t, "send a revoked invitation anew", call(s.main, http.MethodPost,
		invitesPath(ws)+"/"+inv.ID+"/resend", nil, admin.header(true)), http.StatusGone, api.CodeInviteRevoked)
	if got := invitesOf(t, s.main, admin, ws); len(got) != 0 {
		t.Errorf("the revoked invitation is still open: %+v", got)
	}
	if got := workspacesOf(t, s.main, hana); len(got) != 1 {
		t.Errorf("hana belongs to %+v, want her personal workspace alone", got)
	}
	wantCode(t, "use a token that was never made", lookupInvite(t, s.main, "AAAA"), http.StatusBadRequest,
		api.CodeTokenInvalid)
}

// TestInviteEmailMismatch checks that an invitation to an address is refused
// to an account of another address, and to a registration of another
// address, though one of an address that registration refuses is answered
// as registering it without an invitation is, and stays open for its own,
// while a link invitation is accepted by any account.
//
// Verifies: FR-TEN-02/AC3
func TestInviteEmailMismatch(t *testing.T) {
	t.Parallel()
	s, _, admin, ws := inviteServer(t, "open", nil)
	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	token := linkToken(t, createInvite(t, s.main, admin, ws, "hana@example.com", "commenter").URL)
	wantCode(t, "accept another address's invitation", acceptInvite(t, s.main, jo, token), http.StatusForbidden,
		api.CodeInviteEmailMismatch)
	wantCode(t, "register another address with it", registerWithInvite(t, s.main, token, "mo@example.com"),
		http.StatusForbidden, api.CodeInviteEmailMismatch)
	// An address that registration refuses, here for a character that is
	// not printed, is answered as POST /api/v1/auth/register answers it.
	res := registerWithInvite(t, s.main, token, "ha​na@example.com")
	wantCode(t, "register an address that registration refuses", res, http.StatusUnprocessableEntity,
		api.CodeValidationFailed)
	if !strings.Contains(string(res.body), "body.email") {
		t.Errorf("registering an address that registration refuses names no field: %s", res.body)
	}
	if got := workspacesOf(t, s.main, jo); len(got) != 1 {
		t.Errorf("jo belongs to %+v, want his personal workspace alone", got)
	}
	if m := accepted(t, acceptInvite(t, s.main, hana, token)); m.Role != "commenter" {
		t.Errorf("hana's membership is %+v", m)
	}

	link := linkToken(t, createInvite(t, s.main, admin, ws, "", "viewer").URL)
	if m := accepted(t, acceptInvite(t, s.main, jo, link)); m.Role != "viewer" {
		t.Errorf("jo's membership is %+v", m)
	}
}

// TestRemoveLastOwner checks that a workspace keeps an owner: removing its
// last owner whose account is active is refused with 409 last_owner, an
// owner whose account's deletion is pending does not count, and removing one
// of two owners works.
//
// Verifies: FR-TEN-02/AC2
func TestRemoveLastOwner(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "open", nil)
	remove := func(si signedIn, membership string) response {
		return call(s.main, http.MethodDelete, membersPath(ws)+"/"+membership, nil, si.header(true))
	}
	own := membershipOf(t, membersOf(t, s.main, admin, ws), admin)
	wantCode(t, "remove the last owner", remove(admin, own), http.StatusConflict, api.CodeLastOwner)

	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	accepted(t, acceptInvite(t, s.main, hana, linkToken(t, createInvite(t, s.main, admin, ws, "", "owner").URL)))
	hanas := membershipOf(t, membersOf(t, s.main, admin, ws), hana)
	if _, err := d.Admin(t).Exec(context.Background(),
		`UPDATE "user" SET status = 'pending_deletion' WHERE email = 'hana@example.com'`); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "remove the last active owner", remove(admin, own), http.StatusConflict, api.CodeLastOwner)
	if _, err := d.Admin(t).Exec(context.Background(),
		`UPDATE "user" SET status = 'active' WHERE email = 'hana@example.com'`); err != nil {
		t.Fatal(err)
	}
	if res := remove(admin, own); res.status != http.StatusNoContent {
		t.Fatalf("remove one of two owners: %d %s", res.status, res.body)
	}
	wantCode(t, "remove the last owner, who removed the other", remove(hana, hanas), http.StatusConflict,
		api.CodeLastOwner)
	wantCode(t, "the removed owner lists the members", call(s.main, http.MethodGet, membersPath(ws), nil,
		admin.header(false)), http.StatusNotFound, api.CodeNotFound)
	if got := membersOf(t, s.main, hana, ws); len(got) != 1 || got[0].Role != "owner" {
		t.Errorf("the members are %+v, want hana alone", got)
	}
}

// TestOwnerRequired checks that only an owner invites someone as owner,
// sends such an invitation anew, or removes an owner, and that an admin who
// tries is answered with 403 owner_required, while an admin may invite and
// remove the other roles, and revoke an invitation as owner.
//
// Verifies: FR-TEN-03/AC4
func TestOwnerRequired(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	admin := newNamedAccount(t, s.main, "ada@example.com", "Ada")
	join(t, d, admin, ws, "admin")
	ownerInvite := func(si signedIn) response {
		return call(s.main, http.MethodPost, invitesPath(ws), jsonBody(t, map[string]string{"role": "owner"}),
			si.header(true))
	}
	wantCode(t, "an admin invites an owner", ownerInvite(admin), http.StatusForbidden, api.CodeOwnerRequired)
	inv := createInvite(t, s.main, owner, ws, "", "owner")
	wantCode(t, "an admin sends an owner's invitation anew", call(s.main, http.MethodPost,
		invitesPath(ws)+"/"+inv.ID+"/resend", nil, admin.header(true)), http.StatusForbidden, api.CodeOwnerRequired)
	if res := call(s.main, http.MethodDelete, invitesPath(ws)+"/"+inv.ID, nil,
		admin.header(true)); res.status != http.StatusNoContent {
		t.Errorf("an admin revokes an owner's invitation: %d %s", res.status, res.body)
	}

	members := membersOf(t, s.main, admin, ws)
	wantCode(t, "an admin removes an owner", call(s.main, http.MethodDelete,
		membersPath(ws)+"/"+membershipOf(t, members, owner), nil, admin.header(true)), http.StatusForbidden,
		api.CodeOwnerRequired)
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	accepted(t, acceptInvite(t, s.main, jo, linkToken(t, createInvite(t, s.main, admin, ws, "", "admin").URL)))
	if res := call(s.main, http.MethodDelete, membersPath(ws)+"/"+membershipOf(t, membersOf(t, s.main, admin, ws), jo),
		nil, admin.header(true)); res.status != http.StatusNoContent {
		t.Errorf("an admin removes an admin: %d %s", res.status, res.body)
	}
	if res := ownerInvite(owner); res.status != http.StatusCreated {
		t.Errorf("an owner invites an owner: %d %s", res.status, res.body)
	}
	if res := call(s.main, http.MethodDelete, membersPath(ws)+"/"+membershipOf(t, members, admin), nil,
		owner.header(true)); res.status != http.StatusNoContent {
		t.Errorf("an owner removes an admin: %d %s", res.status, res.body)
	}
}

// TestRemoveRevokesInvites checks that removing a member revokes the
// workspace's open invitations that the member made, those whose link the
// member was given by sending them anew, and those to the member's address,
// so that none lets them back in, and records each revocation with the
// removal's actor and membership, while the other invitations stay open.
func TestRemoveRevokesInvites(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	ada := newNamedAccount(t, s.main, "ada@example.com", "Ada")
	join(t, d, ada, ws, "admin")
	adas := linkToken(t, createInvite(t, s.main, ada, ws, "", "admin").URL)
	// The owner's link invitation, whose new link ada takes by sending it
	// anew.
	owners := createInvite(t, s.main, owner, ws, "", "editor")
	res := call(s.main, http.MethodPost, invitesPath(ws)+"/"+owners.ID+"/resend", nil, ada.header(true))
	var resent inviteBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &resent) != nil {
		t.Fatalf("ada sends the owner's invitation anew: %d %s", res.status, res.body)
	}
	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	hanas := linkToken(t, createInvite(t, s.main, owner, ws, "HANA@example.com", "editor").URL)
	// hana joins another way, so the invitation to her address stays open.
	join(t, d, hana, ws, "viewer")
	jo := createInvite(t, s.main, owner, ws, "jo@example.com", "viewer")

	members := membersOf(t, s.main, owner, ws)
	removed := map[string]string{}
	for name, si := range map[string]signedIn{"ada": ada, "hana": hana} {
		removed[name] = membershipOf(t, members, si)
		if res := call(s.main, http.MethodDelete, membersPath(ws)+"/"+removed[name], nil,
			owner.header(true)); res.status != http.StatusNoContent {
			t.Fatalf("remove %s: %d %s", name, res.status, res.body)
		}
	}
	wantCode(t, "the removed admin accepts the invitation that she made", acceptInvite(t, s.main, ada, adas),
		http.StatusGone, api.CodeInviteRevoked)
	wantCode(t, "the removed admin accepts the invitation that she sent anew", acceptInvite(t, s.main, ada,
		linkToken(t, resent.URL)), http.StatusGone, api.CodeInviteRevoked)
	wantCode(t, "the removed member accepts the invitation to her address", acceptInvite(t, s.main, hana, hanas),
		http.StatusGone, api.CodeInviteRevoked)
	if got := invitesOf(t, s.main, owner, ws); len(got) != 1 || got[0].ID != jo.ID {
		t.Errorf("the open invitations are %+v, want jo's alone", got)
	}
	for name, want := range map[string]int{"ada": 2, "hana": 1} {
		n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'invite.revoke'
			AND actor_user_id = $1::uuid AND metadata->>'membership_id' = $2`,
			decode(t, ids.User, owner.body.UserID).String(), decode(t, ids.Membership, removed[name]).String())
		if n != want {
			t.Errorf("the removal of %s revoked %d invitations, want %d", name, n, want)
		}
	}
}

// TestMembersAndInvitesByRole checks who may do what with a workspace's
// members and invitations: every member with a role lists the members, and
// only owners and admins see their email addresses; only owners and admins
// list, make, send anew, and revoke invitations and remove members, and a
// guest or a user who is not a member is told that the workspace does not
// exist.
func TestMembersAndInvitesByRole(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "open", nil)
	accounts := map[string]signedIn{}
	for _, r := range []string{"editor", "viewer", "guest", "none"} {
		si := newNamedAccount(t, s.main, r+"@example.com", strings.ToUpper(r[:1])+r[1:])
		if r != "none" {
			join(t, d, si, ws, r)
		}
		accounts[r] = si
	}
	for _, r := range []string{"editor", "viewer"} {
		members := membersOf(t, s.main, accounts[r], ws)
		if len(members) != 4 {
			t.Errorf("the %s lists %d members, want 4: %+v", r, len(members), members)
		}
		for _, m := range members {
			if m.Email != "" || m.DisplayName == "" || m.Role == "" {
				t.Errorf("the %s sees the member %+v", r, m)
			}
		}
	}
	for _, m := range membersOf(t, s.main, admin, ws) {
		if !strings.HasSuffix(m.Email, "@example.com") {
			t.Errorf("the owner sees no address of %+v", m)
		}
	}

	inv := createInvite(t, s.main, admin, ws, "", "viewer")
	member := membershipOf(t, membersOf(t, s.main, admin, ws), accounts["viewer"])
	for r, status := range map[string]int{"editor": http.StatusForbidden, "viewer": http.StatusForbidden,
		"guest": http.StatusNotFound, "none": http.StatusNotFound} {
		si := accounts[r]
		code := api.CodeInsufficientRole
		if status == http.StatusNotFound {
			code = api.CodeNotFound
			wantCode(t, r+" lists the members", call(s.main, http.MethodGet, membersPath(ws), nil, si.header(false)),
				status, code)
		}
		for what, res := range map[string]response{
			"lists the invitations": call(s.main, http.MethodGet, invitesPath(ws), nil, si.header(false)),
			"invites": call(s.main, http.MethodPost, invitesPath(ws),
				jsonBody(t, map[string]string{"role": "viewer"}), si.header(true)),
			"sends an invitation anew": call(s.main, http.MethodPost, invitesPath(ws)+"/"+inv.ID+"/resend", nil,
				si.header(true)),
			"revokes an invitation": call(s.main, http.MethodDelete, invitesPath(ws)+"/"+inv.ID, nil,
				si.header(true)),
			"removes a member": call(s.main, http.MethodDelete, membersPath(ws)+"/"+member, nil, si.header(true)),
		} {
			wantCode(t, r+" "+what, res, status, code)
		}
	}
	if got := invitesOf(t, s.main, admin, ws); len(got) != 1 {
		t.Errorf("the invitations are %+v, want the one that the owner made", got)
	}
}

// TestInviteConflicts checks the invitations that are refused: one to the
// address of a member, a second open one to an address, as citext compares
// them, until the first is revoked, and one whose fields are not valid; and
// that accepting an invitation of a workspace that the user belongs to
// already is refused, as is sending it anew, and leaves the invitation open.
func TestInviteConflicts(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "open", nil)
	invite := func(email string) response {
		return call(s.main, http.MethodPost, invitesPath(ws),
			jsonBody(t, map[string]string{"email": email, "role": "editor"}), admin.header(true))
	}
	wantCode(t, "invite a member's address", invite("ADMIN@example.com"), http.StatusConflict,
		api.CodeAlreadyMember)
	first := createInvite(t, s.main, admin, ws, "hana@example.com", "editor")
	wantCode(t, "invite an address twice", invite("Hana@Example.com"), http.StatusConflict, api.CodeInviteExists)
	if res := call(s.main, http.MethodDelete, invitesPath(ws)+"/"+first.ID, nil,
		admin.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	second := createInvite(t, s.main, admin, ws, "Hana@Example.com", "editor")
	res := invite("not an address")
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(string(res.body), "body.email") {
		t.Errorf("invite something that is not an address: %d %s", res.status, res.body)
	}
	res = call(s.main, http.MethodPost, invitesPath(ws), jsonBody(t, map[string]string{"role": "none"}),
		admin.header(true))
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(string(res.body), "body.role") {
		t.Errorf("invite with no role: %d %s", res.status, res.body)
	}

	hana := newNamedAccount(t, s.main, "hana@example.com", "Hana")
	join(t, d, hana, ws, "viewer")
	wantCode(t, "accept an invitation as a member", acceptInvite(t, s.main, hana, linkToken(t, second.URL)),
		http.StatusConflict, api.CodeAlreadyMember)
	wantCode(t, "send a member's invitation anew", call(s.main, http.MethodPost,
		invitesPath(ws)+"/"+second.ID+"/resend", nil, admin.header(true)), http.StatusConflict, api.CodeAlreadyMember)
	if got := invitesOf(t, s.main, admin, ws); len(got) != 1 || got[0].ID != second.ID {
		t.Errorf("the open invitations are %+v, want the second one", got)
	}
	wantCode(t, "remove a membership that the workspace does not have", call(s.main, http.MethodDelete,
		membersPath(ws)+"/"+ids.Encode(ids.Membership, ids.New()), nil, admin.header(true)), http.StatusNotFound,
		api.CodeNotFound)
	wantCode(t, "revoke an invitation that the workspace does not have", call(s.main, http.MethodDelete,
		invitesPath(ws)+"/"+ids.Encode(ids.Invite, ids.New()), nil, admin.header(true)), http.StatusNotFound,
		api.CodeNotFound)
}

// TestRegisterWithInvite checks registering an account with an invitation
// under the invite signup policy, which refuses registering without one: the
// new account joins the workspace in the same request, and accepting the
// invitation once it signs in answers with its membership. Registering the
// address of an account that exists answers the same, leaves the account
// and the invitation as they were, and the account then accepts the
// invitation itself. A link invitation needs an address. Under the closed
// policy, an invitation registers no account, whatever the request holds.
//
// Verifies: FR-AUTH-01/AC1
func TestRegisterWithInvite(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "invite", nil)
	wantCode(t, "register without an invitation", call(s.main, http.MethodPost, registerPath,
		registration(t, "hana@example.com", "Hana", testPassword), nil), http.StatusForbidden, api.CodeInviteRequired)
	token := linkToken(t, createInvite(t, s.main, admin, ws, "hana@example.com", "editor").URL)
	created := registerWithInvite(t, s.main, token, "")
	if created.status != http.StatusAccepted || len(created.body) != 0 {
		t.Fatalf("register with the invitation: %d %s", created.status, created.body)
	}
	hana := signIn(t, s.main, "hana@example.com")
	if m := accepted(t, acceptInvite(t, s.main, hana, token)); m.ID != ws || m.Role != "editor" {
		t.Errorf("hana's membership is %+v", m)
	}

	// The address has an account now: a second invitation to it registers
	// nothing, and answers the same.
	other := workspaceFrom(t, call(s.main, http.MethodPost, workspacesPath,
		jsonBody(t, map[string]string{"name": "Second"}), admin.header(true)), http.StatusCreated)
	second := linkToken(t, createInvite(t, s.main, admin, other.ID, "hana@example.com", "viewer").URL)
	accountRow := func() string {
		var row string
		if err := d.Admin(t).QueryRow(context.Background(),
			`SELECT u::text FROM "user" u WHERE email = 'hana@example.com'`).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	before, accounts := accountRow(), countAccounts(t, d)
	body := map[string]string{"token": second, "email": "HANA@example.com", "display_name": "Mallory",
		"password": "another long passphrase"}
	existing := call(s.main, http.MethodPost, registerInvitePath, jsonBody(t, body), nil)
	for _, h := range []http.Header{created.header, existing.header} {
		h.Del(requestIDHeader)
		dropCounts(h)
	}
	if existing.status != created.status || string(existing.body) != string(created.body) ||
		!maps.EqualFunc(existing.header, created.header, slices.Equal) {
		t.Errorf("registering an address that has an account answers %d %v %s, want %d %v %s", existing.status,
			existing.header, existing.body, created.status, created.header, created.body)
	}
	if after := accountRow(); after != before || countAccounts(t, d) != accounts {
		t.Errorf("the account was %s, and is %s, of %d accounts, want %d", before, after, countAccounts(t, d),
			accounts)
	}
	if res := send(s.main, http.MethodPost, loginPath, credentials(t, "hana@example.com", "another long passphrase"),
		"", newClient()); res.status != http.StatusUnauthorized {
		t.Errorf("sign in with the second registration's password: %d %s", res.status, res.body)
	}
	if got := invitesOf(t, s.main, admin, other.ID); len(got) != 1 {
		t.Errorf("the second invitation is not open: %+v", got)
	}
	if m := accepted(t, acceptInvite(t, s.main, hana, second)); m.ID != other.ID || m.Role != "viewer" {
		t.Errorf("hana's second membership is %+v", m)
	}

	link := linkToken(t, createInvite(t, s.main, admin, ws, "", "commenter").URL)
	if res := registerWithInvite(t, s.main, link, ""); res.status != http.StatusUnprocessableEntity ||
		!strings.Contains(string(res.body), "body.email") {
		t.Errorf("register with a link invitation and no address: %d %s", res.status, res.body)
	}
	if res := registerWithInvite(t, s.main, link, "mo@example.com"); res.status != http.StatusAccepted {
		t.Fatalf("register with a link invitation: %d %s", res.status, res.body)
	}
	if m := accepted(t, acceptInvite(t, s.main, signIn(t, s.main, "mo@example.com"), link)); m.Role != "commenter" {
		t.Errorf("mo's membership is %+v", m)
	}

	closed, _, _, _ := inviteServer(t, "closed", nil)
	wantCode(t, "register under closed", call(closed.main, http.MethodPost, registerInvitePath, []byte("{}"), nil),
		http.StatusForbidden, api.CodeSignupClosed)
}

// TestEmailedInvite checks that an instance that sends email emails the link
// of an invitation to an address, which names the instance and the role but
// not the workspace, and gives no link as text then, that sending the
// invitation anew emails a new link, in place of the old one, 10 minutes
// after the last email at the earliest, that registering with the emailed
// link joins the workspace and emails the new account a link that verifies
// its address, and that a link invitation gives its link as text, as it is
// emailed to no one.
func TestEmailedInvite(t *testing.T) {
	t.Parallel()
	server := emailtest.Start(t, emailtest.Options{})
	s, d, admin, ws := inviteServer(t, "open", smtpEnv(server))
	startWorkers(t, s, d)
	inv := createInvite(t, s.main, admin, ws, "hana@example.com", "commenter")
	if inv.URL != "" {
		t.Errorf("an emailed invitation gave its link as text: %+v", inv)
	}
	invitations := func(n int) []emailtest.Message {
		var out []emailtest.Message
		for deadline := time.Now().Add(30 * time.Second); len(out) < n; time.Sleep(20 * time.Millisecond) {
			out = out[:0]
			for _, m := range server.Messages() {
				if strings.Contains(m.Body(t), "/app/invite#token=") {
					out = append(out, m)
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d invitations were emailed, want %d", len(out), n)
			}
		}
		return out
	}
	first := invitations(1)[0]
	if first.Header(t).Get("Subject") != "You are invited to a Storycreep workspace" ||
		len(first.To) != 1 || first.To[0] != "hana@example.com" || !strings.Contains(first.Body(t), "as a commenter") ||
		!strings.Contains(first.Body(t), "within 14 days") || strings.Contains(first.Body(t), "Harbour") {
		t.Errorf("the invitation's email is %v %s", first.To, first.Body(t))
	}
	if res := lookupInvite(t, s.main, first.Token(t)); res.status != http.StatusOK {
		t.Errorf("read the emailed invitation: %d %s", res.status, res.body)
	}
	if got := invitesOf(t, s.main, admin, ws); len(got) != 1 || got[0].SentAt == nil {
		t.Errorf("the open invitations are %+v, want the emailed one", got)
	}

	// The first email was sent 10 minutes ago, as far as the server can
	// tell, so the new link is emailed at once.
	if _, err := d.Admin(t).Exec(context.Background(),
		"UPDATE invite SET sent_at = sent_at - interval '10 minutes'"); err != nil {
		t.Fatal(err)
	}
	res := call(s.main, http.MethodPost, invitesPath(ws)+"/"+inv.ID+"/resend", nil, admin.header(true))
	var renewed inviteBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &renewed) != nil || renewed.URL != "" ||
		renewed.SentAt != nil {
		t.Fatalf("send it anew: %d %s", res.status, res.body)
	}
	wantCode(t, "use the replaced link", lookupInvite(t, s.main, first.Token(t)), http.StatusBadRequest,
		api.CodeTokenInvalid)
	second := invitations(2)[1]
	// hana registers with the emailed link, in a transaction of the
	// workspace, which also emails her account a link that verifies its
	// address, as registering without an invitation does.
	if res := registerWithInvite(t, s.main, second.Token(t), ""); res.status != http.StatusAccepted {
		t.Fatalf("register with the emailed invitation: %d %s", res.status, res.body)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if slices.ContainsFunc(server.Messages(), func(m emailtest.Message) bool {
			return slices.Equal(m.To, []string{"hana@example.com"}) &&
				strings.Contains(m.Body(t), "/app/verify-email#token=")
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the account registered with the invitation was emailed no link that verifies its address")
		}
	}
	hana := signIn(t, s.main, "hana@example.com")
	if m := accepted(t, acceptInvite(t, s.main, hana, second.Token(t))); m.Role != "commenter" {
		t.Errorf("hana's membership is %+v", m)
	}

	link := createInvite(t, s.main, admin, ws, "", "viewer")
	if link.URL == "" || link.Kind != "link" {
		t.Errorf("the link invitation is %+v", link)
	}
	if n := countRows(t, d, "SELECT count(*) FROM river_job WHERE kind = 'email_invite'"); n != 2 {
		t.Errorf("%d invitation emails were enqueued, want 2", n)
	}

	// An invitation sent anew at once after its email is emailed again only
	// 10 minutes after it.
	jo := createInvite(t, s.main, admin, ws, "jo@example.com", "viewer")
	invitations(3)
	if res := call(s.main, http.MethodPost, invitesPath(ws)+"/"+jo.ID+"/resend", nil,
		admin.header(true)); res.status != http.StatusOK {
		t.Fatalf("send jo's invitation anew: %d %s", res.status, res.body)
	}
	if n := countRows(t, d, `SELECT count(*) FROM river_job WHERE kind = 'email_invite' AND state = 'scheduled'
		AND scheduled_at > now() + interval '9 minutes'`); n != 1 {
		t.Errorf("%d invitation emails wait 10 minutes, want the one sent anew at once", n)
	}
}

// TestInviteAuditEvents checks the entries of an invitation's life and of
// the memberships that it adds and that are removed: each names its actor,
// its workspace, and its subject, and none the invited address.
//
// Verifies: FR-TEN-07/AC2
func TestInviteAuditEvents(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "invite", nil)
	revoked := createInvite(t, s.main, admin, ws, "jo@example.com", "viewer")
	if res := call(s.main, http.MethodDelete, invitesPath(ws)+"/"+revoked.ID, nil,
		admin.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	inv := createInvite(t, s.main, admin, ws, "hana@example.com", "editor")
	res := call(s.main, http.MethodPost, invitesPath(ws)+"/"+inv.ID+"/resend", nil, admin.header(true))
	var renewed inviteBody
	if res.status != http.StatusOK || json.Unmarshal(res.body, &renewed) != nil {
		t.Fatalf("send it anew: %d %s", res.status, res.body)
	}
	if res := registerWithInvite(t, s.main, linkToken(t, renewed.URL), ""); res.status != http.StatusAccepted {
		t.Fatalf("register with the invitation: %d %s", res.status, res.body)
	}
	hana := signIn(t, s.main, "hana@example.com")
	link := createInvite(t, s.main, admin, ws, "", "owner")
	// hana is a member already, so the link invitation stays open, and the
	// owner removes her instead.
	wantCode(t, "accept a second invitation", acceptInvite(t, s.main, hana, linkToken(t, link.URL)),
		http.StatusConflict, api.CodeAlreadyMember)
	member := membershipOf(t, membersOf(t, s.main, admin, ws), hana)
	if res := call(s.main, http.MethodDelete, membersPath(ws)+"/"+member, nil,
		admin.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("remove hana: %d %s", res.status, res.body)
	}

	adminID, hanaID := decode(t, ids.User, admin.body.UserID), decode(t, ids.User, hana.body.UserID)
	wsID := decode(t, ids.Workspace, ws)
	for _, c := range []struct {
		action, actor, subject, metadata string
		want                             int
	}{
		{"invite.create", adminID.String(), "", `{"role": "viewer", "kind": "email", "emailed": false}`, 1},
		{"invite.create", adminID.String(), "", `{"role": "editor", "kind": "email", "emailed": false}`, 1},
		{"invite.create", adminID.String(), "", `{"role": "owner", "kind": "link", "emailed": false}`, 1},
		{"invite.revoke", adminID.String(), decode(t, ids.Invite, revoked.ID).String(),
			`{"role": "viewer", "kind": "email"}`, 1},
		{"invite.resend", adminID.String(), decode(t, ids.Invite, inv.ID).String(),
			`{"role": "editor", "kind": "email", "emailed": false}`, 1},
		{"invite.accept", hanaID.String(), decode(t, ids.Invite, inv.ID).String(),
			`{"role": "editor", "kind": "email", "new_account": true}`, 1},
		{"membership.create", hanaID.String(), decode(t, ids.Membership, member).String(),
			`{"role": "editor", "invite_id": "` + decode(t, ids.Invite, inv.ID).String() + `"}`, 1},
		{"membership.remove", adminID.String(), decode(t, ids.Membership, member).String(),
			`{"role": "editor", "user_id": "` + hanaID.String() + `"}`, 1},
	} {
		n := countRows(t, d, `SELECT count(*) FROM audit_entry
			WHERE action = $1 AND actor_user_id = $2 AND workspace_id = $3 AND result = 'success'
				AND ($4 = '' OR subject_id = $4::uuid) AND metadata @> $5::jsonb`,
			c.action, c.actor, wsID, c.subject, c.metadata)
		if n != c.want {
			t.Errorf("%d entries of %s %s, want %d", n, c.action, c.metadata, c.want)
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'invite.accept'
		AND metadata->>'membership_id' = $1`, decode(t, ids.Membership, member).String()); n != 1 {
		t.Errorf("the acceptance names its membership in %d entries, want 1", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE metadata::text LIKE '%@example.com%'`); n != 0 {
		t.Errorf("%d entries name an email address", n)
	}
}
