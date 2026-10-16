package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// databaseHelp says, in the help of a command that connects to the database
// as the server does, how it connects.
const databaseHelp = `
It connects to the database as the server does, as the login in
STORYCREEP_DATABASE_URL, and fails unless the database has every migration
applied, which 'storycreep migrate' does.`

var createUser = &command{
	name:    "create-user",
	summary: "Create an account",
	args:    "<email> <display-name>",
	help: `
Creates an account with the email address and the display name, and the
password that it reads from standard input: its first line, without the
line break. The password must have at least 12 characters, and must not be
one of the 10,000 most common passwords. A password typed at a terminal is
shown as it is typed, so pipe it in instead, as in

  storycreep create-user jo@example.com "Jo Novak" < password.txt

The account is created whatever the signup policy, and whether or not
first-run setup has completed, which it does not complete. With
--instance-admin, the account is an instance admin, which the audit log
records. The account gets its personal workspace at its first sign-in, as
every account does, and 'storycreep grant-role' makes it a member of other
workspaces.

It fails if the email address or the display name is not valid, if the
password breaks the policy, or if the address has an account already.
` + databaseHelp,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.Bool("instance-admin", false, "make the account an instance admin")
	},
	run: func(ctx context.Context, inv *invocation) error {
		if len(inv.args) != 2 {
			return usageError{"expected an email address and a display name"}
		}
		email, name := inv.args[0], inv.args[1]
		password, err := readPassword(inv, email)
		if err != nil {
			return err
		}
		db, err := openDatabase(ctx, inv)
		if err != nil {
			return err
		}
		defer db.Close()
		admin := inv.boolFlag("instance-admin")
		id, err := account.Create(ctx, db, account.NewHasher(),
			account.Registration{Email: email, DisplayName: name, Password: password}, admin)
		if err != nil {
			return createUserError(err)
		}
		kind := "account"
		if admin {
			kind = "instance admin's account"
		}
		fmt.Fprintf(inv.stdout, "Created the %s %s, with the ID %s.\n", kind, strings.TrimSpace(email),
			ids.Encode(ids.User, id))
		return nil
	},
}

var resetPassword = &command{
	name:    "reset-password",
	summary: "Print a link that resets an account's password",
	args:    "<email>",
	help: `
Prints a link that resets the password of the active account that the email
address belongs to. Whoever opens it chooses the account's new password,
once, within an hour, which signs the account out everywhere. So give it to
the account's user alone, by a way that only they can read.

The link opens STORYCREEP_PUBLIC_URL, with its token after the URL's #,
which a browser does not send to the server. It is printed whether or not
the instance sends email: an instance that does emails its users their own
links when they ask, and an instance admin who has no access to the server
takes one with POST /api/v1/admin/password-reset-links, on an instance that
does not. The audit log records the link's issue.

It fails if the address belongs to no account, or to one that is not
active, as a suspended one.
` + databaseHelp,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		if len(inv.args) != 1 {
			return usageError{"expected an email address"}
		}
		db, err := openDatabase(ctx, inv)
		if err != nil {
			return err
		}
		defer db.Close()
		link, err := account.IssueResetLink(ctx, db, inv.cfg.PublicURL, inv.args[0])
		if err != nil {
			return findError(err)
		}
		fmt.Fprintf(inv.stdout, "This link resets the password of %s, once, until %s:\n\n  %s\n", inv.args[0],
			link.ExpiresAt.UTC().Format("2006-01-02 15:04 MST"), link.URL)
		return nil
	},
}

// readPassword reads a password from the command's standard input: its first
// line, without the line break, of at most account.MaxPasswordLength
// characters. If standard input is a terminal, it asks for the password of
// the account email first, on standard error.
func readPassword(inv *invocation, email string) (secret.Value, error) {
	if f, ok := inv.stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintf(inv.stderr, "The password of %s, which is shown as you type it: ", email)
		}
	}
	// A character has at most 4 bytes in UTF-8, and the line break 2, so a
	// first line that fills the limit without its line break is too long,
	// even when the limit cuts a character in two.
	const limit = 4*account.MaxPasswordLength + 3
	r := bufio.NewReader(io.LimitReader(inv.stdin, limit))
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return secret.Value{}, fmt.Errorf("read the password from standard input: %w", err)
	}
	tooLong := len(line) == limit && !strings.HasSuffix(line, "\n")
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch {
	case line == "":
		return secret.Value{}, errors.New("standard input holds no password; pipe the password in, as in " +
			"'storycreep create-user jo@example.com \"Jo Novak\" < password.txt'")
	case !tooLong && !utf8.ValidString(line):
		return secret.Value{}, errors.New("the password is not UTF-8 text")
	case tooLong || utf8.RuneCountInString(line) > account.MaxPasswordLength:
		return secret.Value{}, fmt.Errorf("the password has more than %d characters", account.MaxPasswordLength)
	}
	return secret.New(line), nil
}

// createUserError explains an error of account.Create, without the password.
func createUserError(err error) error {
	var fields input.FieldErrors
	var weak *account.PasswordError
	switch {
	case errors.As(err, &fields):
		names := map[string]string{"email": "the email address", "display_name": "the display name"}
		msgs := make([]string, len(fields))
		for i, f := range fields {
			msgs[i] = names[f.Field] + " " + f.Message
		}
		return errors.New(strings.Join(msgs, "; "))
	case errors.As(err, &weak):
		return errors.New("the password " + weak.Problem())
	case errors.Is(err, account.ErrEmailTaken):
		return errors.New("the email address has an account already")
	}
	return err
}

// instanceAdminRole is the role of grant-role that makes an account an
// instance admin.
const instanceAdminRole = "instance-admin"

// workspaceRoles are the roles of a membership, which grant-role gives in a
// workspace.
var workspaceRoles = []role.Role{role.Owner, role.Admin, role.Editor, role.Commenter, role.Viewer, role.Guest}

var grantRole = &command{
	name:    "grant-role",
	summary: "Give an account a role in a workspace, or make it an instance admin",
	args:    "<email> <role> [workspace]",
	help: `
Gives the account that the email address belongs to a role.

A workspace role, owner, admin, editor, commenter, viewer, or guest, is
given in the workspace whose slug is the third argument: the account becomes
a member of the workspace with the role, or its membership's role changes to
it. A workspace keeps an owner, so the role of its last owner whose account
is active changes only to owner. The workspace's audit log records the
change.

The role instance-admin, without a workspace, makes the account an instance
admin, who will manage the instance on the instance-admin screens, and, while
STORYCREEP_INSTANCE_ADMIN_ACCESS is on, acts as the owner of every workspace
and project. With --revoke, the account is no longer one. The audit log
records the change.

It fails if the address belongs to no account, or to one that is not active,
as a suspended one, or if the workspace does not exist. --revoke is the
exception: it takes instance admin from an account whatever its status, so
that a suspended account does not get it back when it is active again. An
account that has the role already stays as it is.
` + databaseHelp,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.Bool("revoke", false, "with the role instance-admin, make the account no longer an instance admin")
	},
	run: func(ctx context.Context, inv *invocation) error {
		if len(inv.args) < 2 || len(inv.args) > 3 {
			return usageError{"expected an email address, a role, and, for a workspace role, a workspace's slug"}
		}
		email, name := inv.args[0], inv.args[1]
		revoke := inv.boolFlag("revoke")
		if name == instanceAdminRole {
			if len(inv.args) == 3 {
				return usageError{"the role instance-admin belongs to no workspace, so it takes no workspace"}
			}
			return grantInstanceAdmin(ctx, inv, email, !revoke)
		}
		r := role.Role(name)
		switch {
		case !slices.Contains(workspaceRoles, r):
			return usageError{fmt.Sprintf("unknown role %q; a role is owner, admin, editor, commenter, viewer, "+
				"guest, or instance-admin", name)}
		case revoke:
			return usageError{"--revoke takes only the role instance-admin"}
		case len(inv.args) != 3:
			return usageError{fmt.Sprintf("the role %s is a workspace's, so it needs the workspace's slug", name)}
		}
		return grantWorkspaceRole(ctx, inv, email, r, inv.args[2])
	},
}

// grantInstanceAdmin makes the account email an instance admin, or no longer
// one, as grant-role does.
func grantInstanceAdmin(ctx context.Context, inv *invocation, email string, admin bool) error {
	db, err := openDatabase(ctx, inv)
	if err != nil {
		return err
	}
	defer db.Close()
	changed, err := account.SetInstanceAdmin(ctx, db, email, admin)
	if err != nil {
		return findError(err)
	}
	switch {
	case !changed && admin:
		fmt.Fprintf(inv.stdout, "%s is an instance admin already.\n", email)
	case !changed:
		fmt.Fprintf(inv.stdout, "%s is not an instance admin.\n", email)
	case admin:
		fmt.Fprintf(inv.stdout, "%s is now an instance admin.\n", email)
	default:
		fmt.Fprintf(inv.stdout, "%s is no longer an instance admin.\n", email)
	}
	return nil
}

// grantWorkspaceRole gives the account email the role r in the workspace
// slug, as grant-role does.
func grantWorkspaceRole(ctx context.Context, inv *invocation, email string, r role.Role, slug string) error {
	db, err := openDatabase(ctx, inv)
	if err != nil {
		return err
	}
	defer db.Close()
	userID, err := account.Find(ctx, db, email)
	if err != nil {
		return findError(err)
	}
	// The policy resolves no role here: the operator acts for themselves.
	workspaces := workspace.New(db, workspace.Options{})
	id, err := workspaces.BySlug(ctx, slug)
	if errors.Is(err, workspace.ErrNotFound) {
		return fmt.Errorf("no workspace has the slug %q", slug)
	}
	if err != nil {
		return err
	}
	previous, err := workspaces.Grant(ctx, id, userID, r)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return fmt.Errorf("no workspace has the slug %q", slug)
	case errors.Is(err, workspace.ErrLastOwner):
		return fmt.Errorf("%s is the last owner of %s, which must keep one; make another member owner first",
			email, slug)
	case err != nil:
		return err
	case previous == r:
		fmt.Fprintf(inv.stdout, "%s is %s of %s already.\n", email, article(r), slug)
	case previous == role.None:
		fmt.Fprintf(inv.stdout, "%s is now a member of %s, as %s.\n", email, slug, article(r))
	default:
		fmt.Fprintf(inv.stdout, "%s is now %s of %s, instead of %s.\n", email, article(r), slug, article(previous))
	}
	return nil
}

// article returns the role r with its indefinite article, as "an owner".
func article(r role.Role) string {
	if strings.ContainsRune("aeiou", rune(r[0])) {
		return "an " + string(r)
	}
	return "a " + string(r)
}

// findError explains an error of account.Find or account.SetInstanceAdmin.
func findError(err error) error {
	switch {
	case errors.Is(err, account.ErrNoAccount):
		return errors.New("the email address belongs to no account")
	case errors.Is(err, account.ErrNotActive):
		return errors.New("the email address belongs to an account that is not active, as a suspended one")
	}
	return err
}

// openDatabase connects to the database as the application login, as the
// server does, and checks that the database has every migration applied.
func openDatabase(ctx context.Context, inv *invocation) (*database.DB, error) {
	db, err := database.Open(ctx, inv.cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pending, err := db.Pending(ctx)
	switch {
	case err != nil:
		db.Close()
		return nil, err
	case len(pending) > 0:
		db.Close()
		return nil, errors.New("the database has pending migrations; apply them with 'storycreep migrate' first")
	}
	return db, nil
}
