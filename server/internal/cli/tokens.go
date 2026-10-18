package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/apitoken"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/httpserver"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

var createToken = &command{
	name:    "create-token",
	summary: "Make an API token for an account",
	args:    "<email> <workspace> <name>",
	help: `
Makes an API token for the account that the email address belongs to, in the
workspace whose slug is the second argument, with the name that the third
argument gives it, and prints its secret, which starts with scp_, alone on
standard output, and what it made on standard error. The secret is printed
once: the database keeps only its SHA-256 hash. A program sends it as the
bearer token of its requests, in an Authorization: Bearer header, as a CI
job that pulls the workspace's releases does, so that a script can keep it,
as in

  TOKEN=$(storycreep create-token --scopes snapshot:read jo@example.com harbour CI)

--scopes lists the token's scopes, separated by commas: snapshot:read,
project:read, project:write, snapshot:write, and export:read. project:read
implies snapshot:read, project:write implies project:read, and
snapshot:write implies snapshot:read. A token that must not see unreleased
work holds snapshot:read alone. --project narrows the token to one project
of the workspace, by its ID, and --expires makes it expire at a time in RFC
3339, such as 2027-01-31T00:00:00Z.

The account must be an active member of the workspace. The operator acts for
themselves, so the account's role does not limit what the token is made with,
but every request made with it does no more than the account's role allows at
the time, and fails once the account's membership is removed. The workspace's
audit log records the token's making.

It fails if the address belongs to no account, or to one that is not active,
if the workspace does not exist or the account is not a member of it, if the
project does not exist in the workspace or is in its trash, or if the name or
the expiry is not valid.
` + databaseHelp,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.String("scopes", "", "the token's scopes, separated by commas (required)")
		fs.String("project", "", "the ID of the one project of the workspace that the token is narrowed to")
		fs.String("expires", "", "when the token expires, in RFC 3339")
	},
	run: func(ctx context.Context, inv *invocation) error {
		if len(inv.args) != 3 {
			return usageError{"expected an email address, a workspace's slug, and the token's name"}
		}
		email, slug, name := inv.args[0], inv.args[1], inv.args[2]
		spec := apitoken.Spec{Name: name}
		var err error
		if spec.Scopes, err = parseScopes(inv.stringFlag("scopes")); err != nil {
			return err
		}
		var projectID ids.UUID
		if v := inv.stringFlag("project"); v != "" {
			if projectID, err = ids.Decode(ids.Project, v); err != nil {
				return usageError{fmt.Sprintf("--project %q is not a project's ID: %v", v, err)}
			}
		}
		if v := inv.stringFlag("expires"); v != "" {
			expires, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return usageError{fmt.Sprintf("--expires %q is not a time in RFC 3339, such as 2027-01-31T00:00:00Z",
					v)}
			}
			spec.ExpiresAt = &expires
		}
		db, err := openDatabase(ctx, inv)
		if err != nil {
			return err
		}
		defer db.Close()
		userID, err := account.Find(ctx, db, email)
		if err != nil {
			return findError(err)
		}
		workspaces := workspace.New(db, workspace.Options{
			Entitlements: entitlements.New(httpserver.EntitlementOptions(inv.cfg))})
		workspaceID, err := workspaces.BySlug(ctx, slug)
		if errors.Is(err, workspace.ErrNotFound) {
			return fmt.Errorf("no workspace has the slug %q", slug)
		}
		if err != nil {
			return err
		}
		issued, err := apitoken.New(db, apitoken.Options{}).Issue(ctx, userID, workspaceID, projectID, spec)
		var fields input.FieldErrors
		switch {
		case errors.Is(err, apitoken.ErrNotFound) && projectID != ids.UUID{}:
			return fmt.Errorf("%s is not a member of %s, or %s is no project of it, or is in its trash", email, slug,
				ids.Encode(ids.Project, projectID))
		case errors.Is(err, apitoken.ErrNotFound):
			return fmt.Errorf("%s is not a member of %s", email, slug)
		case errors.As(err, &fields):
			msgs := make([]string, len(fields))
			for i, f := range fields {
				field := f.Field
				if field == "expires_at" {
					field = "expiry"
				}
				msgs[i] = fmt.Sprintf("the token's %s %s", field, f.Message)
			}
			return errors.New(strings.Join(msgs, "; "))
		case err != nil:
			return err
		}
		where := slug
		if projectID != (ids.UUID{}) {
			where = ids.Encode(ids.Project, projectID) + ", of " + slug + ","
		}
		fmt.Fprintf(inv.stderr, "Made the API token %s of %s for %s, with the scopes %s. Its secret, which is "+
			"shown only now, follows on standard output.\n", ids.Encode(ids.APIToken, issued.ID), where, email,
			strings.Join(scope.Names(issued.Scopes), ", "))
		fmt.Fprintln(inv.stdout, issued.Secret.Reveal())
		return nil
	},
}

// parseScopes returns the scopes of --scopes, which lists them separated by
// commas, or a usageError if it lists none, or one that is not a scope, as
// grant-role's for a role that is not one. The token keeps each scope once,
// as Issue makes it.
func parseScopes(list string) ([]scope.Scope, error) {
	var out []scope.Scope
	for _, name := range strings.Split(list, ",") {
		s := scope.Scope(strings.TrimSpace(name))
		switch {
		case s == "":
			continue
		case !s.Known():
			return nil, usageError{fmt.Sprintf("unknown scope %q; a scope is %s", s,
				strings.Join(scope.Names(scope.All()), ", "))}
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, usageError{"--scopes lists no scope; a token holds one at least"}
	}
	return out, nil
}
