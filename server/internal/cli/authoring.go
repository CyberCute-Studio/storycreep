package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	cmds "github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/httpserver"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

var exportProject = &command{
	name:    "export",
	summary: "Export a project's authoring data to a zip",
	args:    "<email> <project-id>",
	help: `
Exports a project as a zip of its authoring data, in the format of
schema/authoring/v1, to the file that --output names, as the account that the
email address belongs to, who must be able to read the project. It holds the
project's draft, or the release that --release names by its ID, with its
comments, play states, release history, and assets.

A full export contains personal data: the comments, and the display names of
the people who wrote them, saved play states, muted findings, or published
releases. --profile content leaves all of it out. --no-assets and --no-releases
leave out the project's assets and its release history.

It fails if the address belongs to no account, or to one that is not active,
if the account cannot read the project, or if the file exists already.
` + databaseHelp,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.String("output", "", "the file to write the archive to, which must not exist (required)")
		fs.String("release", "", "the ID of the ready release to export, instead of the draft")
		fs.String("profile", "full", "full, or content, which leaves out comments, play states, and who did what")
		fs.Bool("no-assets", false, "leave out the project's assets")
		fs.Bool("no-releases", false, "leave out the project's release history")
	},
	run: func(ctx context.Context, inv *invocation) error {
		if len(inv.args) != 2 {
			return usageError{"expected an email address and a project's ID"}
		}
		email := inv.args[0]
		projectID, err := ids.Decode(ids.Project, inv.args[1])
		if err != nil {
			return usageError{fmt.Sprintf("%q is not a project's ID: %v", inv.args[1], err)}
		}
		output := inv.stringFlag("output")
		if output == "" {
			return usageError{"--output is required"}
		}
		req := authoring.Request{Profile: authoring.Profile(inv.stringFlag("profile")),
			IncludeAssets: !inv.boolFlag("no-assets"), IncludeReleases: !inv.boolFlag("no-releases")}
		if req.Profile != authoring.ProfileFull && req.Profile != authoring.ProfileContent {
			return usageError{fmt.Sprintf("--profile %q is neither full nor content", req.Profile)}
		}
		if v := inv.stringFlag("release"); v != "" {
			id, err := ids.Decode(ids.Snapshot, v)
			if err != nil {
				return usageError{fmt.Sprintf("--release %q is not a release's ID: %v", v, err)}
			}
			req.SnapshotID = &id
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
		service, closeTools, err := httpserver.AuthoringTools(ctx, inv.cfg, logging.New(inv.stderr, inv.cfg.LogLevel), db)
		if err != nil {
			return err
		}
		defer closeTools()
		f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		e, err := service.ExportFile(ctx, cmds.Actor{UserID: userID}, projectID, req, f)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(output)
			return authoringFailure(err)
		}
		fmt.Fprintf(inv.stderr, "Exported %s to %s, %d bytes.\n", ids.Encode(ids.Project, projectID), output,
			valueOr(e.SizeBytes))
		if e.PersonalData {
			fmt.Fprintln(inv.stderr, "This is a full export, and it contains personal data: handle it accordingly.")
		}
		return nil
	},
}

// valueOr returns the value of p, or 0.
func valueOr(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

var importProject = &command{
	name:    "import",
	summary: "Import a project from an authoring export",
	args:    "<email> <workspace> <archive.zip>",
	help: `
Imports an authoring export as a new project of the workspace whose slug is
the second argument, as the account that the email address belongs to, who
must be an owner or an admin of it, and prints the new project's ID on
standard output. The archive is checked whole before anything is written, and
the import is all or nothing: its zip against the guards for path traversal and
zip bombs, each file against its hash and its schema, and every release's
files, hashes, references, and ASTs. A section that this server cannot import
refuses the archive, and is named.

It keeps the archive's IDs, unless one of them is in use on this instance, or
--remap asks for fresh ones. People whom the archive names and who are not
members of the workspace are shown as imported users, with the display names
from the archive. It needs STORYCREEP_DATABASE_MAINTENANCE_URL, for the check of
the IDs.

It fails, and creates nothing, if the address belongs to no account, or to one
that is not active, if the workspace does not exist, if the account may not
create a project in it, if the workspace is at its projects limit, if the file
is larger than STORYCREEP_IMPORT_MAX_BYTES, or if the archive is refused.
` + databaseHelp,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.Bool("remap", false, "give everything fresh IDs, whether or not the archive's are in use")
	},
	run: func(ctx context.Context, inv *invocation) error {
		if len(inv.args) != 3 {
			return usageError{"expected an email address, a workspace's slug, and the archive's path"}
		}
		email, slug, path := inv.args[0], inv.args[1], inv.args[2]
		if !inv.cfg.DatabaseMaintenanceURL.IsSet() {
			return errors.New("STORYCREEP_DATABASE_MAINTENANCE_URL is not set: an import needs the maintenance " +
				"login, which migrating provisions, to check the archive's IDs")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%s is not a file with an archive in it", path)
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
		service, closeTools, err := httpserver.AuthoringTools(ctx, inv.cfg, logging.New(inv.stderr, inv.cfg.LogLevel), db)
		if err != nil {
			return err
		}
		defer closeTools()
		name := filepath.Base(path)
		if chars := []rune(name); len(chars) > 255 {
			name = string(chars[len(chars)-255:])
		}
		i, err := service.ImportFile(ctx, cmds.Actor{UserID: userID}, workspaceID, name, io.ReaderAt(f), info.Size(),
			inv.boolFlag("remap"))
		if err != nil {
			return authoringFailure(err)
		}
		if i.Failure != nil {
			return fmt.Errorf("the archive was refused, and nothing was imported: %s", describeFailure(i.Failure))
		}
		r := i.Report
		fmt.Fprintf(inv.stderr, "Imported %q with the ID policy %s: %d boards, %d nodes, %d variables, %d components, "+
			"%d comments, %d play states, %d releases, and %d assets. %d people are shown as imported users.\n",
			r.ProjectName, r.Policy, r.Boards, r.Nodes, r.Variables, r.Components, r.Comments, r.PlayStates, r.Releases,
			r.Assets, r.ImportedUsers)
		if r.MissingAssets > 0 {
			fmt.Fprintf(inv.stderr, "The archive holds no assets, so %d references to them were not kept.\n",
				r.MissingAssets)
		}
		fmt.Fprintln(inv.stdout, r.ProjectID)
		return nil
	},
}

// describeFailure says why an import was refused.
func describeFailure(f *authoring.ImportError) string {
	out := f.Code
	if f.Section != "" {
		out += ", section " + f.Section
	}
	if f.Version != "" {
		out += ", release " + f.Version
	}
	if f.Check != "" {
		out += ", check " + f.Check
	}
	if f.Detail != "" {
		out += ": " + f.Detail
	}
	return out
}

// authoringFailure returns err as the command line says it.
func authoringFailure(err error) error {
	var limit *entitlements.LimitError
	var tooLarge *authoring.ImportTooLargeError
	var inProgress *authoring.ImportInProgressError
	var notReady *snapshot.NotReadyError
	var fields input.FieldErrors
	switch {
	case errors.As(err, &limit):
		return fmt.Errorf("the workspace is at its %s limit of %d", limit.Key, limit.Limit)
	case errors.As(err, &tooLarge):
		return fmt.Errorf("the archive has %d bytes, more than the %d of STORYCREEP_IMPORT_MAX_BYTES", tooLarge.Size,
			tooLarge.Max)
	case errors.As(err, &inProgress):
		return fmt.Errorf("the account has the import %s of this workspace on its way already", inProgress.ID)
	case errors.As(err, &notReady):
		return fmt.Errorf("the release is %s, and only a ready one can be exported", notReady.Status)
	case errors.Is(err, snapshot.ErrNotFound):
		return errors.New("the project has no such release")
	case errors.Is(err, project.ErrNotFound):
		return errors.New("no such project or workspace for this account")
	case errors.Is(err, authz.ErrInsufficientRole):
		return errors.New("the account's role does not allow this")
	case errors.As(err, &fields) && len(fields) > 0:
		return fmt.Errorf("the %s %s", fields[0].Field, fields[0].Message)
	}
	return err
}
