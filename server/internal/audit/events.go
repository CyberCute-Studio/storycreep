package audit

// Action names a type of event in the audit log, as its entries record it.
// An action, once released, keeps its meaning, because exports of the log
// name it (PRD FR-TEN-08).
type Action string

// The actions. Each is registered in events. The PR that adds a feature
// whose events FR-TEN-07 lists registers them here, and records them (plan
// item 12, standing checklist).
const (
	// ActionLogin is an attempt to sign in with an email address and a
	// password (FR-AUTH-02). It belongs to no workspace, because an account
	// signs in to all of its workspaces at once. Its subject is the account
	// that the address belongs to, if any, and a failed attempt for an
	// address that belongs to no account records the address as its keyed
	// hash instead (Entry.AttemptedEmail). A successful one names the
	// session that it started in its metadata's session_id, and a failed
	// one says why in its metadata's reason: invalid_credentials, or
	// locked, when too many failures in a row had locked the address, and
	// the password was not checked (FR-AUTH-05).
	ActionLogin Action = "auth.login"
	// ActionSessionRevoke is the end of a session before its time
	// (FR-AUTH-02): its user signed out of it, revoked it, or changed the
	// account's password, which revokes the account's other sessions. Its
	// metadata's reason says which: sign_out, revoked, or password_changed.
	// It belongs to no workspace, as a login does.
	ActionSessionRevoke Action = "auth.session_revoke"
	// ActionPasswordChange is an attempt by a signed-in user to change the
	// account's password, which fails when the current password given is
	// not the account's. It belongs to no workspace.
	ActionPasswordChange Action = "auth.password_change"
)

// Result is how an event ended.
type Result string

// The results.
const (
	// ResultSuccess is an action that happened.
	ResultSuccess Result = "success"
	// ResultFailure is an action that was attempted but did not happen, as
	// a login with a wrong password does not.
	ResultFailure Result = "failure"
	// ResultDenied is an action that was refused for want of a permission,
	// as the use of a revoked API token is (FR-TEN-07).
	ResultDenied Result = "denied"
)

// eventSpec registers a type of event: what Record allows its entries to
// hold.
type eventSpec struct {
	action Action
	// inWorkspace says that each event belongs to a workspace. An event that
	// belongs to none, such as a login, is recorded without one, in a
	// transaction without one.
	inWorkspace bool
	// subject is the kind of entity that the event is about, as the name of
	// the table that the subject's ID is a row of, or "" for an event
	// without a subject. An entry of an event with a subject may still lack
	// one, as a failed login for an address of no account does.
	subject string
	// results are the results that the event can have.
	results []Result
	// metadata are the keys that an entry's metadata may hold.
	metadata []string
	// attemptedEmail says that a failed event may record an email address
	// that belongs to no account, as its keyed hash (Entry.AttemptedEmail).
	attemptedEmail bool
}

// events is the registry of event types.
var events = []eventSpec{
	{action: ActionLogin, subject: "user", results: []Result{ResultSuccess, ResultFailure},
		metadata: []string{"session_id", "reason"}, attemptedEmail: true},
	{action: ActionSessionRevoke, subject: "session", results: []Result{ResultSuccess}, metadata: []string{"reason"}},
	{action: ActionPasswordChange, subject: "user", results: []Result{ResultSuccess, ResultFailure}},
}

// lookup returns the registration of a, and false if a is not registered.
func lookup(a Action) (eventSpec, bool) {
	for _, s := range events {
		if s.action == a {
			return s, true
		}
	}
	return eventSpec{}, false
}
