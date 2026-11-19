package role

import (
	"fmt"
	"testing"
)

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

// TestGrants checks that Grants lists exactly the roles that a grant of a
// member resolves to on the project, Effective's own reading of what a grant
// can be: every role that allows something but owner.
func TestGrants(t *testing.T) {
	t.Parallel()
	var want []Role
	for _, g := range grants {
		if g != None && (Policy{}).Effective(false, Viewer, g) == g {
			want = append(want, g)
		}
	}
	if got := Grants(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Grants() = %v, want %v", got, want)
	}
}

// TestReaders checks that Readers lists the roles of a membership that read
// a project without a grant, every one but guest's, and those of a grant that
// read it, every grant's, as Effective resolves them for a user who is no
// instance admin, so that the queries that filter by them read the projects
// as Effective does: any workspace role but guest, or a guest with a grant.
func TestReaders(t *testing.T) {
	t.Parallel()
	memberships, grants := Readers()
	if want := []Role{Owner, Admin, Editor, Commenter, Viewer}; fmt.Sprint(memberships) != fmt.Sprint(want) {
		t.Errorf("the memberships that read a project without a grant are %v, want %v", memberships, want)
	}
	if want := Grants(); fmt.Sprint(grants) != fmt.Sprint(want) {
		t.Errorf("the grants that read a project are %v, want %v", grants, want)
	}
	reads := func(m, g Role) bool {
		if g != None {
			for _, r := range grants {
				if r == g {
					return true
				}
			}
			return false
		}
		for _, r := range memberships {
			if r == m {
				return true
			}
		}
		return false
	}
	for _, m := range Memberships() {
		for _, g := range append([]Role{None}, Grants()...) {
			if got, want := reads(m, g), (Policy{}).Effective(false, m, g) != None; got != want {
				t.Errorf("membership %s, grant %s: the lists read %v, Effective %v", m, g, got, want)
			}
		}
	}
}

// TestReaderNames checks that ReaderNames names the roles of Readers, in
// their order.
func TestReaderNames(t *testing.T) {
	t.Parallel()
	memberships, grants := Readers()
	names, grantNames := ReaderNames()
	if fmt.Sprint(names) != fmt.Sprint(memberships) || fmt.Sprint(grantNames) != fmt.Sprint(grants) {
		t.Errorf("ReaderNames() = %v, %v, want %v, %v", names, grantNames, memberships, grants)
	}
}
