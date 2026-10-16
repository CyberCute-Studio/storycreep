package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// TestUnattendedSetupKeys checks the keys of unattended setup: none set is
// no unattended setup, the four that it needs set are one, with the
// password a secret, and any of them set without the others that it needs
// is a problem for each missing one.
func TestUnattendedSetupKeys(t *testing.T) {
	if c := mustLoad(t, withRequired(nil)); c.Setup != nil {
		t.Errorf("Setup = %+v without the keys", c.Setup)
	}
	all := map[string]string{
		"STORYCREEP_SETUP_OWNER_EMAIL":    "jo@example.com",
		"STORYCREEP_SETUP_OWNER_NAME":     "Jo Novak",
		"STORYCREEP_SETUP_OWNER_PASSWORD": "correct horse battery staple",
		"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour Studio",
	}
	c := mustLoad(t, withRequired(all))
	want := UnattendedSetup{OwnerEmail: "jo@example.com", OwnerName: "Jo Novak", WorkspaceName: "Harbour Studio"}
	if got := *c.Setup; got.OwnerPassword.Reveal() != "correct horse battery staple" {
		t.Errorf("the password is %q", got.OwnerPassword.Reveal())
	} else if got.OwnerPassword = (secret.Value{}); got != want {
		t.Errorf("Setup = %+v, want %+v", got, want)
	}
	if e := entry(t, c, "STORYCREEP_SETUP_OWNER_PASSWORD"); e.Value != secret.Redacted {
		t.Errorf("the password's entry is %+v", e)
	}
	more := withRequired(all)
	more["STORYCREEP_SETUP_WORKSPACE_SLUG"], more["STORYCREEP_SETUP_SIGNUP"] = "harbour", "invite"
	if c := mustLoad(t, more); c.Setup.WorkspaceSlug != "harbour" || c.Setup.Signup != SignupInvite {
		t.Errorf("Setup = %+v", c.Setup)
	}

	for _, c := range []struct {
		set  map[string]string
		want []string
	}{
		{map[string]string{"STORYCREEP_SETUP_OWNER_EMAIL": "jo@example.com"}, []string{"STORYCREEP_SETUP_OWNER_NAME",
			"STORYCREEP_SETUP_OWNER_PASSWORD", "STORYCREEP_SETUP_WORKSPACE_NAME"}},
		{map[string]string{"STORYCREEP_SETUP_SIGNUP": "open"}, []string{"STORYCREEP_SETUP_OWNER_EMAIL",
			"STORYCREEP_SETUP_OWNER_NAME", "STORYCREEP_SETUP_OWNER_PASSWORD", "STORYCREEP_SETUP_WORKSPACE_NAME"}},
		{map[string]string{"STORYCREEP_SETUP_OWNER_EMAIL": "jo@example.com", "STORYCREEP_SETUP_OWNER_NAME": "Jo",
			"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour", "STORYCREEP_SETUP_SIGNUP": "everyone"},
			[]string{"STORYCREEP_SETUP_SIGNUP", "STORYCREEP_SETUP_OWNER_PASSWORD"}},
	} {
		problems := loadProblems(t, Options{Env: MapEnvironment(withRequired(c.set))})
		if got := problemKeys(problems); !slices.Equal(got, c.want) {
			t.Errorf("%v: the problems name %q, want %q", c.set, got, c.want)
		}
		for _, p := range problems {
			if p.Key == "STORYCREEP_SETUP_OWNER_PASSWORD" && !strings.Contains(p.Msg, "required with the other "+
				"STORYCREEP_SETUP_ keys") {
				t.Errorf("%v: %s", c.set, p)
			}
		}
	}
}

// TestSignupResolve checks which policy applies under each Signup
// capability, given the policy that first-run setup chose.
func TestSignupResolve(t *testing.T) {
	for _, c := range []struct {
		capability, chosen, want SignupPolicy
	}{
		{SignupOpen, SignupClosed, SignupOpen},
		{SignupInvite, "", SignupInvite},
		{SignupClosed, SignupOpen, SignupClosed},
		{SignupSetup, SignupOpen, SignupOpen},
		{SignupSetup, SignupInvite, SignupInvite},
		{SignupSetup, SignupClosed, SignupClosed},
		{SignupSetup, "", SignupClosed},
		{SignupSetup, "everyone", SignupClosed},
		{SignupSetup, SignupSetup, SignupClosed},
		{"", SignupOpen, SignupClosed},
	} {
		if got := c.capability.Resolve(c.chosen); got != c.want {
			t.Errorf("%q.Resolve(%q) = %q, want %q", c.capability, c.chosen, got, c.want)
		}
	}
}
