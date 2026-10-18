package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// usagePath returns the path of the usage of the workspace ws.
func usagePath(ws string) string { return workspacesPath + "/" + ws + "/usage" }

// readUsage reads the usage of the workspace ws as si, which must be 200,
// as each key's JSON object.
func readUsage(t *testing.T, h http.Handler, si signedIn, ws string) map[string]map[string]any {
	t.Helper()
	res := call(h, http.MethodGet, usagePath(ws), nil, si.header(false))
	var body map[string]map[string]any
	if res.status != http.StatusOK || json.Unmarshal(res.body, &body) != nil {
		t.Fatalf("read the usage: %d %s, want 200", res.status, res.body)
	}
	return body
}

// wantMembersLimit fails the test unless res is 409 limit_exceeded for the
// members limit of limit, which the workspace uses all of, without an
// upgrade link, as a self-hosted instance's cap never has.
func wantMembersLimit(t *testing.T, what string, res response, limit float64) {
	t.Helper()
	wantCode(t, what, res, http.StatusConflict, api.CodeLimitExceeded)
	d := res.details(t)
	if d["key"] != "members" || d["usage"] != limit || d["limit"] != limit || d["upgrade_url"] != nil {
		t.Errorf("%s: details %v, want the members limit of %v reached, without an upgrade link", what, d, limit)
	}
}

// TestMembersLimitThroughInvitations checks that a workspace that has as many
// members as STORYCREEP_WORKSPACE_LIMITS caps it at refuses to let in another
// by an invitation, with 409 limit_exceeded naming the limit and the usage,
// and keeps the invitation open: accepting it refuses, and registering with
// it answers 202 alike for a new address and for one with an account, adding
// the new address's account but not the member, who is refused on accepting
// once signed in, and let in once a member has left, and leaving the account
// that the other address has as it was. Inviting is not refused.
//
// Verifies: FR-ENT-01/AC1, FR-AUTH-01/AC1
func TestMembersLimitThroughInvitations(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "members=2"})
	first := linkToken(t, createInvite(t, s.main, admin, ws, "", "editor").URL)
	second := linkToken(t, createInvite(t, s.main, admin, ws, "", "viewer").URL)
	emailed := linkToken(t, createInvite(t, s.main, admin, ws, "bo@example.com", "commenter").URL)
	existing := linkToken(t, createInvite(t, s.main, admin, ws, "al@example.com", "commenter").URL)

	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	accepted(t, acceptInvite(t, s.main, jo, first))
	al := newNamedAccount(t, s.main, "al@example.com", "Al")
	wantMembersLimit(t, "accept past the limit", acceptInvite(t, s.main, al, second), 2)

	// A new address, and one that has an account, which are answered alike.
	accounts := countAccounts(t, d)
	for _, token := range []string{emailed, existing} {
		if res := registerWithInvite(t, s.main, token, ""); res.status != http.StatusAccepted || len(res.body) != 0 {
			t.Errorf("register with an invitation to a full workspace: %d %s, want 202 without a body", res.status,
				res.body)
		}
	}
	if n := countAccounts(t, d); n != accounts+1 {
		t.Errorf("registering with invitations to a full workspace left %d accounts, want %d, the new address's",
			n, accounts+1)
	}
	if got := membersOf(t, s.main, admin, ws); len(got) != 2 {
		t.Errorf("the full workspace has %d members, want 2", len(got))
	}
	if got := invitesOf(t, s.main, admin, ws); len(got) != 3 {
		t.Errorf("the full workspace has %d open invitations, want the 3 refused", len(got))
	}
	// The account that the address had already is as it was, with its own
	// display name rather than the registration's.
	var name string
	if err := d.Admin(t).QueryRow(context.Background(), `SELECT display_name FROM "user"
		WHERE email = 'al@example.com'`).Scan(&name); err != nil || name != "Al" {
		t.Errorf("after registering at a full workspace, al's account is named %q (%v), want Al", name, err)
	}
	bo := signIn(t, s.main, "bo@example.com")
	wantMembersLimit(t, "accept a registration's invitation past the limit", acceptInvite(t, s.main, bo, emailed), 2)
	// A full workspace still invites.
	createInvite(t, s.main, admin, ws, "", "viewer")

	if res := call(s.main, http.MethodDelete, memberPath(ws, membershipOf(t, membersOf(t, s.main, admin, ws), jo)),
		nil, admin.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("remove jo: %d %s", res.status, res.body)
	}
	if m := accepted(t, acceptInvite(t, s.main, bo, emailed)); m.ID != ws || m.Role != "commenter" {
		t.Errorf("bo's membership, once a member has left, is %+v", m)
	}
	if got := readUsage(t, s.main, admin, ws)["members"]; got["used"] != 2.0 || got["limit"] != 2.0 {
		t.Errorf("the usage of members is %v, want 2 of 2", got)
	}
}

// TestMembersLimitLinkRegistration checks that registering with the link of
// a link invitation, which any address may register with, to a workspace
// that has as many members as STORYCREEP_WORKSPACE_LIMITS caps it at is
// refused with 409 limit_exceeded, which adds no account, and is answered
// alike for an address that has an account, so that under the invite signup
// policy a link that the full workspace keeps open does not register one
// account after another, and that once a member has left, the link registers
// an account that joins the workspace.
//
// Verifies: FR-ENT-01/AC1, FR-AUTH-01/AC1
func TestMembersLimitLinkRegistration(t *testing.T) {
	t.Parallel()
	s, d, admin, ws := inviteServer(t, "invite", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "members=2"})
	first := linkToken(t, createInvite(t, s.main, admin, ws, "", "editor").URL)
	if res := registerWithInvite(t, s.main, first, "jo@example.com"); res.status != http.StatusAccepted {
		t.Fatalf("register jo with a link: %d %s, want 202", res.status, res.body)
	}
	link := linkToken(t, createInvite(t, s.main, admin, ws, "", "viewer").URL)

	// refusal returns the error of an answer, without its request ID, which
	// is each request's own.
	refusal := func(res response) api.ErrorBody {
		t.Helper()
		var env struct {
			Error api.ErrorBody `json:"error"`
		}
		if err := json.Unmarshal(res.body, &env); err != nil {
			t.Fatalf("not an envelope: %s", res.body)
		}
		env.Error.RequestID = ""
		return env.Error
	}
	accounts := countAccounts(t, d)
	var want api.ErrorBody
	// Two new addresses, and the owner's, which has an account.
	for i, email := range []string{"al@example.com", "bo@example.com", "admin@example.com"} {
		res := registerWithInvite(t, s.main, link, email)
		wantMembersLimit(t, "register "+email+" with the link of a full workspace", res, 2)
		if got := refusal(res); i == 0 {
			want = got
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("register %s with the link of a full workspace: %+v, and a new address: %+v", email, got, want)
		}
	}
	if n := countAccounts(t, d); n != accounts {
		t.Errorf("registering with the link of a full workspace left %d accounts, want %d", n, accounts)
	}
	if got := invitesOf(t, s.main, admin, ws); len(got) != 1 {
		t.Errorf("the full workspace has %d open invitations, want the link's", len(got))
	}

	jo := signIn(t, s.main, "jo@example.com")
	if res := call(s.main, http.MethodDelete, memberPath(ws, membershipOf(t, membersOf(t, s.main, admin, ws), jo)),
		nil, admin.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("remove jo: %d %s", res.status, res.body)
	}
	if res := registerWithInvite(t, s.main, link, "al@example.com"); res.status != http.StatusAccepted {
		t.Fatalf("register with the link once a member has left: %d %s, want 202", res.status, res.body)
	}
	membershipOf(t, membersOf(t, s.main, admin, ws), signIn(t, s.main, "al@example.com"))
	if got := readUsage(t, s.main, admin, ws)["members"]; got["used"] != 2.0 || got["limit"] != 2.0 {
		t.Errorf("the usage of members is %v, want 2 of 2", got)
	}
}

// TestMembersLimitLinkRegistrationRace checks that a registration with the
// link of a link invitation that found room in the workspace, but whose
// workspace fills up before the transaction that adds the account, is
// refused all the same, with 409 limit_exceeded, adding no account, for a
// new address as for one that has an account, so that the registrations that
// a link lets through at once do not keep an account each once the
// workspace is full, and the answer still does not tell whether the address
// has one. An open transaction that adds an account with the same address
// holds the registration where it adds the account, while another
// invitation fills the workspace, and then rolls back, leaving the address
// new, or commits, giving it an account.
//
// Verifies: FR-ENT-01/AC1, FR-AUTH-01/AC1
func TestMembersLimitLinkRegistrationRace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d, admin, ws := inviteServer(t, "invite", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "members=2"})
	link := linkToken(t, createInvite(t, s.main, admin, ws, "", "editor").URL)
	// held reports whether a session of the test's own database waits to add
	// an account, as the registration does for the open transaction's.
	watcher := d.Admin(t)
	held := func() bool {
		var n int
		if err := watcher.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() "+
			"AND wait_event_type = 'Lock' AND query LIKE '%name: InsertUser %'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	for i, existing := range []bool{false, true} {
		email := fmt.Sprintf("racer%d@example.com", i)
		tx, err := d.Admin(t).Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, 'Racer')`, ids.New(),
			email); err != nil {
			t.Fatal(err)
		}
		done := make(chan response, 1)
		go func() { done <- registerWithInvite(t, s.main, link, email) }()
		for deadline := time.Now().Add(time.Minute); !held(); time.Sleep(20 * time.Millisecond) {
			select {
			case res := <-done:
				t.Fatalf("the registration of %s was not held where it adds the account: %d %s", email, res.status,
					res.body)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("the registration of %s is never held where it adds the account", email)
			}
		}
		// Another invitation fills the workspace meanwhile.
		filler := fmt.Sprintf("filler%d@example.com", i)
		if res := registerWithInvite(t, s.main, linkToken(t, createInvite(t, s.main, admin, ws, "", "viewer").URL),
			filler); res.status != http.StatusAccepted {
			t.Fatalf("fill the workspace: %d %s, want 202", res.status, res.body)
		}
		if existing {
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		wantMembersLimit(t, fmt.Sprintf("register %s (with an account: %v) once the workspace has filled up", email,
			existing), <-done, 2)
		var accounts int
		var name string
		if err := watcher.QueryRow(ctx, `SELECT count(*), coalesce(max(display_name), '') FROM "user"
			WHERE email = $1`, email).Scan(&accounts, &name); err != nil {
			t.Fatal(err)
		}
		if existing && (accounts != 1 || name != "Racer") || !existing && accounts != 0 {
			t.Errorf("the refused registration of %s (with an account: %v) left %d accounts named %q", email,
				existing, accounts, name)
		}
		// The filler leaves, so that the next registration finds room first.
		if res := call(s.main, http.MethodDelete, memberPath(ws, membershipOf(t, membersOf(t, s.main, admin, ws),
			signIn(t, s.main, filler))), nil, admin.header(true)); res.status != http.StatusNoContent {
			t.Fatalf("remove %s: %d %s", filler, res.status, res.body)
		}
	}
	if got := invitesOf(t, s.main, admin, ws); len(got) != 1 {
		t.Errorf("the workspace has %d open invitations, want the link's", len(got))
	}
}

// TestWorkspaceUsage checks that a workspace's owners and admins read its
// usage of every key that usage meters, with the limit of each that the
// instance caps and the warning from 80% of it, and none for one that it
// does not, and that an editor is refused with 403 insufficient_role and a
// user who is not a member with 404 not_found.
//
// Verifies: FR-ENT-02/AC2
func TestWorkspaceUsage(t *testing.T) {
	t.Parallel()
	s, _, admin, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "members=5, " +
		"storage_bytes=1048576"})
	roles := map[string]signedIn{"owner": admin}
	for _, r := range []string{"admin", "editor"} {
		// Not admin@example.com, which is the owner's.
		si := newNamedAccount(t, s.main, "as-"+r+"@example.com", r)
		accepted(t, acceptInvite(t, s.main, si, linkToken(t, createInvite(t, s.main, admin, ws, "", r).URL)))
		roles[r] = si
	}
	// Two more members: four of five is 80%, which warns.
	accepted(t, acceptInvite(t, s.main, newNamedAccount(t, s.main, "jo@example.com", "Jo"),
		linkToken(t, createInvite(t, s.main, admin, ws, "", "viewer").URL)))
	for _, r := range []string{"owner", "admin"} {
		got := readUsage(t, s.main, roles[r], ws)
		var keys []string
		for k := range got {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var want []string
		for _, k := range usage.Keys() {
			want = append(want, string(k))
		}
		slices.Sort(want)
		if !slices.Equal(keys, want) {
			t.Errorf("as %s, the usage has the keys %v, want %v", r, keys, want)
		}
		for k, wantObj := range map[string]map[string]any{
			"members":       {"used": 4.0, "limit": 5.0, "warning": true},
			"storage_bytes": {"used": 0.0, "limit": 1048576.0, "warning": false},
			"projects":      {"used": 0.0, "warning": false},
			"boards":        {"used": 0.0},
			"nodes":         {"used": 0.0},
			"snapshots":     {"used": 0.0},
		} {
			// The values are scalars, which compare as maps.Equal compares them.
			if gotObj := got[k]; !maps.Equal(gotObj, wantObj) {
				t.Errorf("as %s, the usage of %s is %v, want %v", r, k, gotObj, wantObj)
			}
		}
	}
	wantCode(t, "an editor's usage", call(s.main, http.MethodGet, usagePath(ws), nil, roles["editor"].header(false)),
		http.StatusForbidden, api.CodeInsufficientRole)
	stranger := newNamedAccount(t, s.main, "stranger@example.com", "Stranger")
	wantCode(t, "a stranger's usage", call(s.main, http.MethodGet, usagePath(ws), nil, stranger.header(false)),
		http.StatusNotFound, api.CodeNotFound)
}

// TestUsageBodyKeys checks that the body of a workspace's usage has a field
// of the right kind for every key of usage, so that a key that package usage
// gains cannot be left out of the answer, which fails instead.
func TestUsageBodyKeys(t *testing.T) {
	t.Parallel()
	var us []workspace.Usage
	for _, k := range usage.Keys() {
		limited := entitlements.LimitKey(k).Checked()
		us = append(us, workspace.Usage{Key: k, Used: 1, Limited: limited, Quota: entitlements.AtMost(3)})
	}
	b, err := newUsageBody(us)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, u := range us {
		obj := got[string(u.Key)]
		if obj["used"] != 1.0 || u.Limited != (obj["limit"] == 3.0) {
			t.Errorf("the body gives %s as %v", u.Key, obj)
		}
	}
	if _, err := newUsageBody([]workspace.Usage{{Key: "api_requests"}}); err == nil {
		t.Error("a key that the body has no field of was left out")
	}
	if _, err := newUsageBody([]workspace.Usage{{Key: usage.Nodes, Limited: true}}); err == nil {
		t.Error("a limited key was given a field without a limit")
	}
}
