package authoring

import (
	"archive/zip"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// auditPage is how many entries of the audit log a workspace's archive reads
// at a time, each page in a short transaction of its own, so that it holds
// neither the whole log in memory nor a transaction open while it writes.
const auditPage = 1000

// workspaceContent is what a workspace's build read of it at the start, as of
// one moment: all but its projects' content and its audit log, which it reads
// as it writes them.
type workspaceContent struct {
	manifest WorkspaceManifest
	// projects are those that the archive is to hold, in the order of their
	// IDs, without their sizes and hashes, which writing them gives.
	projects []WorkspaceProject
	members  []byte
	// memberCount is how many members members holds.
	memberCount int
	// assetIndex is the index of the workspace's own assets, and assets
	// their files.
	assetIndex []byte
	assets     []assetFile
	fileName   string
}

// readWorkspace reads what the archive of the workspace export row holds of
// the workspace itself, in one transaction that reads as of one moment.
func (s *Service) readWorkspace(ctx context.Context, row queries.WorkspaceExport) (*workspaceContent, error) {
	c := &workspaceContent{}
	// Before the snapshot, so that the archive holds whatever was committed
	// by the time that the manifest names, as an authoring export's does.
	exportedAt := time.Now().UTC()
	err := s.db.InSnapshot(ctx, database.Scope{WorkspaceID: row.WorkspaceID.Bytes}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		ws, err := q.WorkspaceExportWorkspace(ctx, row.WorkspaceID)
		if err != nil {
			return fmt.Errorf("authoring: read the workspace: %w", err)
		}
		projects, err := q.WorkspaceExportProjects(ctx, row.WorkspaceID)
		if err != nil {
			return fmt.Errorf("authoring: read the workspace's projects: %w", err)
		}
		// Not nil, as the format requires an array of each.
		trashed := []TrashedProject{}
		for _, p := range projects {
			id := ids.Encode(ids.Project, p.ID.Bytes)
			if p.DeletedAt.Valid {
				at := p.DeletedAt.Time.UTC()
				trashed = append(trashed, TrashedProject{ID: id, Name: p.Name, Slug: p.Slug, DeletedAt: &at})
				continue
			}
			c.projects = append(c.projects, WorkspaceProject{ID: id, Name: p.Name, Slug: p.Slug,
				Archived: p.ArchivedAt.Valid, Path: workspaceProjectsDir + id + ".zip"})
		}
		members, err := s.readMembers(ctx, q, row.WorkspaceID)
		if err != nil {
			return err
		}
		c.memberCount = len(members)
		if c.members, err = marshal(Items[WorkspaceMember]{Items: members}); err != nil {
			return fmt.Errorf("authoring: write the members: %w", err)
		}
		assets, err := q.WorkspaceExportAssets(ctx, row.WorkspaceID)
		if err != nil {
			return fmt.Errorf("authoring: read the workspace's assets: %w", err)
		}
		index := make([]Asset, len(assets))
		c.assets = make([]assetFile, len(assets))
		for i, a := range assets {
			id := encode(ids.Asset, a.ID)
			_, body, _ := strings.Cut(id, "_")
			file := "assets/" + body + path.Ext(a.StorageKey)
			index[i] = Asset{ID: id, Path: file, SHA256: hex.EncodeToString(a.Sha256), Mime: a.Mime, Bytes: a.Bytes,
				Width: int4(a.Width), Height: int4(a.Height)}
			c.assets[i] = assetFile{key: a.StorageKey, path: file}
		}
		if c.assetIndex, err = marshal(Items[Asset]{Items: index}); err != nil {
			return fmt.Errorf("authoring: write the workspace's assets: %w", err)
		}
		c.manifest = WorkspaceManifest{Format: WorkspaceFormat, FormatVersion: WorkspaceVersion,
			AuthoringFormatVersion: Version, Generator: serializer.Generator(), ExportedAt: exportedAt,
			Workspace: ManifestWorkspace{ID: ids.Encode(ids.Workspace, ws.ID.Bytes), Name: ws.Name, Slug: ws.Slug,
				CreatedAt: ws.CreatedAt.Time.UTC()},
			PersonalData: true, TrashedProjects: trashed}
		c.fileName = ws.Slug + "-workspace.zip"
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// readMembers returns the members of the workspace ws, each with their grants.
func (s *Service) readMembers(ctx context.Context, q *queries.Queries, ws pgtype.UUID) ([]WorkspaceMember, error) {
	rows, err := q.WorkspaceExportMembers(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("authoring: read the workspace's members: %w", err)
	}
	grants, err := q.WorkspaceExportGrants(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("authoring: read the members' grants: %w", err)
	}
	of := map[ids.UUID][]MemberGrant{}
	for _, g := range grants {
		of[g.MembershipID.Bytes] = append(of[g.MembershipID.Bytes], MemberGrant{
			ProjectID: ids.Encode(ids.Project, g.ProjectID.Bytes), Role: g.Role})
	}
	out := make([]WorkspaceMember, len(rows))
	for i, m := range rows {
		memberGrants := of[m.ID.Bytes]
		if memberGrants == nil {
			memberGrants = []MemberGrant{}
		}
		out[i] = WorkspaceMember{MembershipID: ids.Encode(ids.Membership, m.ID.Bytes),
			UserID: ids.Encode(ids.User, m.UserID.Bytes), DisplayName: m.DisplayName, Email: m.Email, Role: m.Role,
			JoinedAt: m.JoinedAt.Time.UTC(), Grants: memberGrants}
	}
	return out, nil
}

// writeWorkspace writes the archive of c to w: each project's archive, which it
// reads and writes one at a time, as an authoring export of its draft does,
// and the workspace's own assets, then the members, the audit log, which it
// reads a page at a time, the manifest, and the README. A project that went to
// the trash, or was deleted for good, after c was read joins the manifest's
// trashed projects rather than the archive.
func (s *Service) writeWorkspace(ctx context.Context, w io.Writer, store blob.Store, row queries.WorkspaceExport,
	c *workspaceContent) error {
	zw := zip.NewWriter(w)
	// The projects' archives and the assets' files are compressed already:
	// they go in as deflate's stored blocks, as an authoring export's assets
	// do (copyAsset), and the JSON files after them compressed.
	stored, err := flate.NewWriter(io.Discard, flate.NoCompression)
	if err != nil {
		return fmt.Errorf("authoring: start the archives' compressor: %w", err)
	}
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		stored.Reset(out)
		return stored, nil
	})
	// Not nil, as the format requires an array.
	c.manifest.Projects = []WorkspaceProject{}
	for _, p := range c.projects {
		written, err := s.writeProject(ctx, zw, store, row, p)
		if errors.Is(err, errProjectGone) {
			c.manifest.TrashedProjects = append(c.manifest.TrashedProjects,
				TrashedProject{ID: p.ID, Name: p.Name, Slug: p.Slug})
			continue
		}
		if err != nil {
			return err
		}
		c.manifest.Projects = append(c.manifest.Projects, written)
	}
	for _, a := range c.assets {
		if err := copyAsset(ctx, zw, store, a); err != nil {
			return err
		}
	}
	compressed, err := flate.NewWriter(io.Discard, flate.DefaultCompression)
	if err != nil {
		return fmt.Errorf("authoring: start the compressor: %w", err)
	}
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		compressed.Reset(out)
		return compressed, nil
	})
	file := func(name string) (io.Writer, error) {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return nil, fmt.Errorf("authoring: add %s to the archive: %w", name, err)
		}
		return f, nil
	}
	section := func(name, at string, items int, data []byte) error {
		f, err := file(at)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("authoring: write %s to the archive: %w", at, err)
		}
		sum := sha256.Sum256(data)
		c.manifest.Sections = append(c.manifest.Sections, ManifestSection{Name: name, Path: at, Items: items,
			SHA256: hex.EncodeToString(sum[:])})
		return nil
	}
	// Not nil, as the format requires an array.
	c.manifest.Sections = []ManifestSection{}
	if err := section("members", WorkspaceMembersPath, c.memberCount, c.members); err != nil {
		return err
	}
	if err := section("assets", WorkspaceAssetsPath, len(c.assets), c.assetIndex); err != nil {
		return err
	}
	f, err := file(WorkspaceAuditPath)
	if err != nil {
		return err
	}
	h := sha256.New()
	entries, err := s.writeAudit(ctx, io.MultiWriter(f, h), row.WorkspaceID, c.manifest.ExportedAt)
	if err != nil {
		return err
	}
	c.manifest.Sections = append(c.manifest.Sections, ManifestSection{Name: "audit log", Path: WorkspaceAuditPath,
		Items: entries, SHA256: hex.EncodeToString(h.Sum(nil))})
	manifest, err := marshal(c.manifest)
	if err != nil {
		return fmt.Errorf("authoring: write the manifest: %w", err)
	}
	if f, err = file(WorkspaceManifestPath); err != nil {
		return err
	}
	if _, err := f.Write(manifest); err != nil {
		return fmt.Errorf("authoring: write the manifest to the archive: %w", err)
	}
	if f, err = file(ReadmePath); err != nil {
		return err
	}
	if _, err := io.WriteString(f, workspaceReadme(c.manifest)); err != nil {
		return fmt.Errorf("authoring: write the README to the archive: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("authoring: finish the archive: %w", err)
	}
	return nil
}

// writeProject writes the archive of the project p into zw, as a full
// authoring export of its draft with its release history and its assets, and
// returns p with its archive's size and hash. It returns errProjectGone, and
// writes nothing, for a project in the trash.
func (s *Service) writeProject(ctx context.Context, zw *zip.Writer, store blob.Store, row queries.WorkspaceExport,
	p WorkspaceProject) (WorkspaceProject, error) {
	id, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		return p, fmt.Errorf("authoring: read a project's ID: %w", err)
	}
	export := queries.AuthoringExport{WorkspaceID: row.WorkspaceID, ProjectID: uuid(id), Profile: string(ProfileFull),
		IncludeAssets: true, IncludeReleases: true}
	content, err := s.read(ctx, export)
	if err != nil {
		return p, err
	}
	f, err := zw.CreateHeader(&zip.FileHeader{Name: p.Path, Method: zip.Deflate, Modified: zipTime})
	if err != nil {
		return p, fmt.Errorf("authoring: add %s to the archive: %w", p.Path, err)
	}
	h := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(f, h)}
	if err := s.write(ctx, counted, store, export, content); err != nil {
		return p, fmt.Errorf("authoring: write the archive of the project %s: %w", p.ID, err)
	}
	p.SizeBytes, p.SHA256 = counted.n, hex.EncodeToString(h.Sum(nil))
	return p, nil
}

// writeAudit writes the workspace's audit log, of the entries made by until,
// to w, as the items of a section's file, one entry to a line, reading a page
// of them at a time, and returns how many it wrote.
func (s *Service) writeAudit(ctx context.Context, w io.Writer, ws pgtype.UUID, until time.Time) (int, error) {
	if _, err := io.WriteString(w, "{\n  \"items\": ["); err != nil {
		return 0, fmt.Errorf("authoring: write the audit log: %w", err)
	}
	n := 0
	// From before the first entry, which no entry precedes.
	after := queries.WorkspaceExportAuditEntriesParams{WorkspaceID: ws,
		Until:   pgtype.Timestamptz{Time: until, Valid: true},
		AfterAt: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true}, AfterID: pgtype.UUID{Valid: true},
		RowLimit: auditPage}
	for {
		var rows []queries.WorkspaceExportAuditEntriesRow
		err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws.Bytes}, func(tx pgx.Tx) error {
			var err error
			rows, err = queries.New(tx).WorkspaceExportAuditEntries(ctx, after)
			return err
		})
		if err != nil {
			return n, fmt.Errorf("authoring: read the audit log: %w", err)
		}
		for _, e := range rows {
			data, err := marshalLine(auditEntry(e))
			if err != nil {
				return n, fmt.Errorf("authoring: write an entry of the audit log: %w", err)
			}
			sep := ",\n    "
			if n == 0 {
				sep = "\n    "
			}
			if _, err := io.WriteString(w, sep); err != nil {
				return n, fmt.Errorf("authoring: write the audit log: %w", err)
			}
			if _, err := w.Write(data); err != nil {
				return n, fmt.Errorf("authoring: write the audit log: %w", err)
			}
			n++
		}
		if len(rows) < auditPage {
			break
		}
		last := rows[len(rows)-1]
		after.AfterAt, after.AfterID = last.CreatedAt, last.ID
	}
	end := "]\n}\n"
	if n > 0 {
		end = "\n  ]\n}\n"
	}
	if _, err := io.WriteString(w, end); err != nil {
		return n, fmt.Errorf("authoring: write the audit log: %w", err)
	}
	return n, nil
}

// marshalLine writes v as JSON on one line, without escaping what HTML would
// need, as marshal does.
func marshalLine(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(b.String(), "\n")), nil
}

// auditEntry returns the entry e as the archive holds it.
func auditEntry(e queries.WorkspaceExportAuditEntriesRow) WorkspaceAuditEntry {
	out := WorkspaceAuditEntry{ID: ids.UUID(e.ID.Bytes).String(), Action: e.Action, Result: e.Result,
		UserAgent: e.UserAgent.String, RequestID: e.RequestID.String, Metadata: Value(e.Metadata),
		CreatedAt: e.CreatedAt.Time.UTC()}
	if e.ActorUserID.Valid {
		out.ActorUserID = ids.Encode(ids.User, e.ActorUserID.Bytes)
	}
	if e.ActorTokenID.Valid {
		out.ActorTokenID = ids.Encode(ids.APIToken, e.ActorTokenID.Bytes)
	}
	if e.SubjectID.Valid {
		out.SubjectKind = e.SubjectKind.String
		out.SubjectID = ids.UUID(e.SubjectID.Bytes).String()
		for _, k := range ids.Kinds() {
			if k.String() == e.SubjectKind.String {
				out.SubjectID = ids.Encode(k, e.SubjectID.Bytes)
				break
			}
		}
	}
	if e.Ip != nil {
		out.IP = e.Ip.String()
	}
	return out
}

// workspaceReadme returns the archive's README, which says what it holds, and
// that it holds personal data.
func workspaceReadme(m WorkspaceManifest) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("# Storycreep workspace export")
	line("")
	line("This archive holds the workspace %q, in version %d of the format %s, which `workspace.json`",
		m.Workspace.Name, m.FormatVersion, m.Format)
	line("describes and `schema/workspace/v1/archive.schema.json` of the Storycreep repository defines. Its")
	line("reading began at %s.", m.ExportedAt.Format(time.RFC3339))
	line("")
	line("## Personal data")
	line("")
	line("**This archive contains personal data.** `members.json` holds the display name and the email address")
	line("of every member of the workspace, `audit-log.json` the IP address and the user agent that each")
	line("recorded action was asked from, and each project's archive its comments, with the names of the")
	line("people who wrote them, saved its play states, or published its releases. Handle and store this file")
	line("accordingly, and share it only with people who may see that data.")
	line("")
	line("## What it holds")
	line("")
	line("- `projects/<the project's ID>.zip`: each of the workspace's %d project(s) outside its trash, archived", len(m.Projects))
	line("  ones too, as a full authoring export of its draft, with its comments, play states, release history,")
	line("  and assets, in the format of `schema/authoring`, which a Storycreep project import takes as it is.")
	line("  Each says in its own `authoring.json` when its content was read.")
	for _, s := range m.Sections {
		line("- `%s`: %s, %d item(s)", s.Path, s.Name, s.Items)
	}
	line("")
	if len(m.TrashedProjects) == 0 {
		line("The workspace's trash held no project, so the archive leaves none out.")
	} else {
		line("%d project(s) in the workspace's trash, or that went there or were deleted for good while the", len(m.TrashedProjects))
		line("archive was made, are not in it. `workspace.json` names them, under `trashedProjects`.")
	}
	line("")
	line("The audit log is the one that the workspace keeps, for as long as its retention keeps each entry.")
	return b.String()
}
