package role

import "testing"

// memberships are the roles that a membership can have, None for a user who
// is not a member, and a value that is no role, which must fail closed.
var memberships = []Role{None, Owner, Admin, Editor, Commenter, Viewer, Guest, "superuser", ""}

// grants are the roles that a grant can have, None for a membership without
// one, and values that a grant cannot have.
var grants = []Role{None, Admin, Editor, Commenter, Viewer, Owner, Guest, "superuser", ""}

// TestWorkspace checks workspace_role (PRD §5.5) over every combination of
// the instance's policy, whether the user is an instance admin, and their
// membership, absent included: a user who is not a member, or is a guest,
// resolves to none, and so is answered with 404.
//
// Verifies: FR-TEN-04/AC3, FR-TEN-04/AC6
func TestWorkspace(t *testing.T) {
	t.Parallel()
	want := map[Role]Role{
		None: None, Owner: Owner, Admin: Admin, Editor: Editor, Commenter: Commenter, Viewer: Viewer,
		Guest: None, "superuser": None, "": None,
	}
	for _, access := range []bool{false, true} {
		for _, instanceAdmin := range []bool{false, true} {
			for _, m := range memberships {
				w := want[m]
				if access && instanceAdmin {
					w = Owner
				}
				p := Policy{InstanceAdminAccess: access}
				if got := p.Workspace(instanceAdmin, m); got != w {
					t.Errorf("access %v, instance admin %v, membership %q: %q, want %q", access, instanceAdmin, m, got,
						w)
				}
			}
		}
	}
}

// TestEffective checks effective_role (PRD §5.5) over every combination of
// the instance's policy, whether the user is an instance admin, their
// membership of the project's workspace, absent included, and the
// membership's grant on the project, absent included. The table is written
// out row by row, a membership's row against each grant, so that it reads
// as the rules do: a grant replaces the membership's role, a guest acts only
// through a grant, a grant is never owner, and a user who is not a member
// resolves to none whatever else holds.
//
// Verifies: FR-TEN-04/AC3, FR-TEN-04/AC6
func TestEffective(t *testing.T) {
	t.Parallel()
	// want[membership] is the resolution for each grant, in the order of
	// grants.
	want := map[Role][]Role{
		None:        {None, None, None, None, None, None, None, None, None},
		Owner:       {Owner, Admin, Editor, Commenter, Viewer, None, None, None, None},
		Admin:       {Admin, Admin, Editor, Commenter, Viewer, None, None, None, None},
		Editor:      {Editor, Admin, Editor, Commenter, Viewer, None, None, None, None},
		Commenter:   {Commenter, Admin, Editor, Commenter, Viewer, None, None, None, None},
		Viewer:      {Viewer, Admin, Editor, Commenter, Viewer, None, None, None, None},
		Guest:       {None, Admin, Editor, Commenter, Viewer, None, None, None, None},
		"superuser": {None, None, None, None, None, None, None, None, None},
		"":          {None, None, None, None, None, None, None, None, None},
	}
	for _, access := range []bool{false, true} {
		for _, instanceAdmin := range []bool{false, true} {
			for _, m := range memberships {
				for i, g := range grants {
					w := want[m][i]
					if access && instanceAdmin {
						w = Owner
					}
					p := Policy{InstanceAdminAccess: access}
					if got := p.Effective(instanceAdmin, m, g); got != w {
						t.Errorf("access %v, instance admin %v, membership %q, grant %q: %q, want %q", access,
							instanceAdmin, m, g, got, w)
					}
				}
			}
		}
	}
}

// TestAtLeast checks the order of the roles: owner > admin > editor >
// commenter > viewer, with guest and none below every role and allowing
// nothing.
func TestAtLeast(t *testing.T) {
	t.Parallel()
	ordered := []Role{Owner, Admin, Editor, Commenter, Viewer}
	for i, r := range ordered {
		for j, least := range ordered {
			if got := r.AtLeast(least); got != (i <= j) {
				t.Errorf("%s.AtLeast(%s) = %v", r, least, got)
			}
		}
		for _, nothing := range []Role{Guest, None, ""} {
			if r.AtLeast(nothing) || nothing.AtLeast(r) {
				t.Errorf("%s and %q are ordered", r, nothing)
			}
		}
	}
}
