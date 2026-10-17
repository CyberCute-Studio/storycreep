package httpserver

import (
	"context"
	"net/http"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestAccessOffRevokesInvites checks the end of instance-admin access at a
// server's start (PRD §5.5): an instance admin who acts as the owner of
// another account's workspace through STORYCREEP_INSTANCE_ADMIN_ACCESS
// invites someone there as owner, and the first server that starts with
// the access off revokes the invitation, so that its link answers 410
// invite_revoked, says so in its log, and records the revocation without an
// actor, while the invitation that the instance admin made in its own
// workspace, as its owner, stays open.
//
// Verifies: FR-TEN-02/AC4
func TestAccessOffRevokesInvites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d, admin, own := inviteServer(t, "open", map[string]string{"STORYCREEP_INSTANCE_ADMIN_ACCESS": "on"})
	if err := s.setupStep(ctx); err != nil {
		t.Fatal(err)
	}
	bo := newNamedAccount(t, s.main, "bo@example.com", "Bo")
	bos := personalWorkspace(t, s.main, bo).ID
	// The setup owner is an instance admin, who acts as owner of Bo's
	// workspace without being a member.
	through := linkToken(t, createInvite(t, s.main, admin, bos, "", "owner").URL)
	kept := linkToken(t, createInvite(t, s.main, admin, own, "", "owner").URL)

	log, buf := newTestLog()
	off := newServer(loadConfig(t, inviteEnv("open", nil)), log)
	off.ready.db.Store(d.Open(t))
	if err := off.setupStep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := buf.records(t, accessEndedMessage); len(got) != 1 || got[0]["invites"] != float64(1) {
		t.Errorf("the start with the access off logged %v, want one record of 1 invitation", got)
	}
	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	wantCode(t, "accept the invitation that the access made", acceptInvite(t, s.main, jo, through),
		http.StatusGone, api.CodeInviteRevoked)
	if res := lookupInvite(t, s.main, kept); res.status != http.StatusOK {
		t.Errorf("the invitation in the instance admin's own workspace: %d %s", res.status, res.body)
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'invite.revoke' AND actor_user_id IS NULL
		AND workspace_id = $1::uuid AND metadata->>'user_id' = $2`, decode(t, ids.Workspace, bos).String(),
		decode(t, ids.User, admin.body.UserID).String()); n != 1 {
		t.Errorf("%d revocations recorded, want 1", n)
	}
}
