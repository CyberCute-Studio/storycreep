package authoring

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// errProjectGone is the error of an export whose project is in its trash, or
// gone, by the time that the job reads it.
var errProjectGone = errors.New("authoring: the project is in its workspace's trash")

// errAssetMissing is the error of an export of an asset whose file the store
// no longer has, which another attempt would only find missing again.
var errAssetMissing = errors.New("authoring: the store has no file of an asset")

// zipTime is the modification time of every member of an archive, the
// earliest that a zip's MS-DOS time holds, as a snapshot export's are, since
// the manifest says when the archive was made.
var zipTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// member is a JSON file of the archive.
type member struct {
	path string
	data []byte
}

// content is what a build read of the project, as of one moment, which it
// then writes without holding a transaction open.
type content struct {
	manifest Manifest
	readme   string
	members  []member
	// release holds the files of the release that the archive is of, by
	// their paths, or is nil for an archive of the draft.
	release map[string][]byte
	// releases are the releases of the history whose files are to be copied,
	// without the metadata that their section has written.
	releases []historyRelease
	assets   []assetFile
	fileName string
}

// historyRelease is what copying a release of the history needs of it.
type historyRelease struct {
	id, version, path string
}

// marshal writes v as a section's file: indented, as a person reads it, and
// without escaping what HTML would need, as the text is not HTML.
func marshal(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// read reads what the archive of row holds of its project, in one transaction
// that reads as of one moment, so that its sections agree with one another. It
// returns errProjectGone for a project in the trash.
func (s *Service) read(ctx context.Context, row queries.AuthoringExport) (*content, error) {
	c := &content{}
	err := s.db.InSnapshot(ctx, database.Scope{WorkspaceID: row.WorkspaceID.Bytes}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		project, err := q.AuthoringProject(ctx, queries.AuthoringProjectParams{WorkspaceID: row.WorkspaceID,
			ID: row.ProjectID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errProjectGone
		}
		if err != nil {
			return fmt.Errorf("authoring: read the project: %w", err)
		}
		r := &reading{q: q, ws: row.WorkspaceID, project: row.ProjectID, profile: Profile(row.Profile),
			live: map[string]bool{}, people: map[ids.UUID]bool{}}
		source := Source{Kind: "draft"}
		suffix := "draft"
		if row.SnapshotID.Valid {
			snapshot, err := q.Snapshot(ctx, queries.SnapshotParams{WorkspaceID: row.WorkspaceID,
				ProjectID: row.ProjectID, ID: row.SnapshotID})
			if err != nil {
				return fmt.Errorf("authoring: read the release: %w", err)
			}
			files, err := q.SnapshotFiles(ctx, queries.SnapshotFilesParams{WorkspaceID: row.WorkspaceID,
				SnapshotID: row.SnapshotID})
			if err != nil {
				return fmt.Errorf("authoring: read the release's files: %w", err)
			}
			c.release = make(map[string][]byte, len(files))
			for _, f := range files {
				c.release[f.Path] = f.Bytes
			}
			if r.release, err = entitiesOf(c.release); err != nil {
				return err
			}
			source = Source{Kind: "release", SnapshotID: ids.Encode(ids.Snapshot, row.SnapshotID.Bytes),
				Version: snapshot.Version, ContentHash: snapshot.ContentHash.String}
			suffix = snapshot.Version
		}
		// Not nil, as an archive of a release without a section still lists
		// them, as the empty array that the format requires.
		entries := []ManifestSection{}
		for _, sec := range sections {
			if !sec.holds(row) {
				continue
			}
			v, items, err := sec.read(ctx, r)
			if err != nil {
				return err
			}
			data, err := marshal(v)
			if err != nil {
				return fmt.Errorf("authoring: write the section %s: %w", sec.Name, err)
			}
			sum := sha256.Sum256(data)
			c.members = append(c.members, member{path: sec.Path, data: data})
			entries = append(entries, ManifestSection{Name: sec.Name, Path: sec.Path, Items: items,
				SHA256: hex.EncodeToString(sum[:])})
		}
		c.assets = r.assets
		c.releases = make([]historyRelease, len(r.releases))
		for i, rel := range r.releases {
			c.releases[i] = historyRelease{id: rel.ID, version: rel.Version, path: rel.Path}
		}
		c.manifest = Manifest{Format: Format, FormatVersion: Version,
			PublishedSchemaVersion: published.SchemaVersion, Generator: serializer.Generator(),
			ExportedAt: time.Now().UTC(), Project: ManifestProject{ID: ids.Encode(ids.Project, row.ProjectID.Bytes),
				Name: project.Name, Slug: project.Slug},
			Source: source, Profile: Profile(row.Profile), PersonalData: Profile(row.Profile) == ProfileFull,
			IncludesAssets: row.IncludeAssets, IncludesReleases: row.IncludeReleases, Sections: entries,
			LeftOut: r.leftOut}
		c.fileName = project.Slug + "-" + suffix + "-authoring.zip"
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.readme = readme(c.manifest)
	return c, nil
}

// write writes the archive of c to w: the README, the manifest, and the
// sections, then the release's files, the release history, and the assets. It
// lets go of the sections and the release's files once they are written, and
// then holds one release of the history at a time in memory, and streams each
// asset. It reads each release of the history in a transaction of its own, as
// a ready snapshot's files never change, so that no transaction stays open for
// as long as the archive takes.
func (s *Service) write(ctx context.Context, w io.Writer, store blob.Store, row queries.AuthoringExport,
	c *content) error {
	zw := zip.NewWriter(w)
	file := func(name string, method uint16, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: zipTime})
		if err != nil {
			return fmt.Errorf("authoring: add %s to the archive: %w", name, err)
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("authoring: write %s to the archive: %w", name, err)
		}
		return nil
	}
	manifest, err := marshal(c.manifest)
	if err != nil {
		return fmt.Errorf("authoring: write the manifest: %w", err)
	}
	if err := file(ReadmePath, zip.Deflate, []byte(c.readme)); err != nil {
		return err
	}
	if err := file(ManifestPath, zip.Deflate, manifest); err != nil {
		return err
	}
	for _, m := range c.members {
		if err := file(m.path, zip.Deflate, m.data); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.release)) {
		if err := file("release/"+name, zip.Deflate, c.release[name]); err != nil {
			return err
		}
	}
	c.members, c.release = nil, nil
	for _, rel := range c.releases {
		id, err := ids.Decode(ids.Snapshot, rel.id)
		if err != nil {
			return fmt.Errorf("authoring: read a release's ID: %w", err)
		}
		var files []queries.SnapshotFilesRow
		err = s.db.InTx(ctx, database.Scope{WorkspaceID: row.WorkspaceID.Bytes}, func(tx pgx.Tx) error {
			var err error
			files, err = queries.New(tx).SnapshotFiles(ctx, queries.SnapshotFilesParams{WorkspaceID: row.WorkspaceID,
				SnapshotID: pgtype.UUID{Bytes: id, Valid: true}})
			return err
		})
		if err != nil {
			return fmt.Errorf("authoring: read the files of release %s: %w", rel.version, err)
		}
		slices.SortFunc(files, func(a, b queries.SnapshotFilesRow) int { return strings.Compare(a.Path, b.Path) })
		for _, f := range files {
			if err := file(rel.path+f.Path, zip.Deflate, f.Bytes); err != nil {
				return err
			}
		}
	}
	for _, a := range c.assets {
		if err := copyAsset(ctx, zw, store, a); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("authoring: finish the archive: %w", err)
	}
	return nil
}

// copyAsset copies an asset's file into the archive as it is, since an image
// is compressed already.
func copyAsset(ctx context.Context, zw *zip.Writer, store blob.Store, a assetFile) error {
	src, _, err := store.Open(ctx, a.key)
	if errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("%w: %s", errAssetMissing, a.path)
	}
	if err != nil {
		return fmt.Errorf("authoring: read the asset's file %s: %w", a.path, err)
	}
	defer func() { _ = src.Close() }()
	f, err := zw.CreateHeader(&zip.FileHeader{Name: a.path, Method: zip.Store, Modified: zipTime})
	if err != nil {
		return fmt.Errorf("authoring: add %s to the archive: %w", a.path, err)
	}
	if _, err := io.Copy(f, src); err != nil {
		return fmt.Errorf("authoring: write %s to the archive: %w", a.path, err)
	}
	return nil
}

// readme returns the archive's README, which says what it holds and, as
// FR-DATA-01 AC6 requires of a full export, that it holds personal data.
func readme(m Manifest) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("# Storycreep authoring export")
	line("")
	line("This archive holds the authoring data of the project %q, in version %d of the format %s, which",
		m.Project.Name, m.FormatVersion, m.Format)
	line("`authoring.json` describes and `schema/authoring/v1/archive.schema.json` of the Storycreep repository")
	line("defines. It was made at %s.", m.ExportedAt.Format(time.RFC3339))
	line("")
	line("## Personal data")
	line("")
	if m.PersonalData {
		line("**This is a full export, and it contains personal data.** `people.json` holds the display names of")
		line("the people who wrote its comments, saved its play states, muted its findings, and published its")
		line("releases, and the comments are what they wrote. Handle and store this file accordingly, and share")
		line("it only with people who may see that data. A content-only export leaves all of it out.")
	} else {
		line("This is a content-only export. It holds no comments, play states, or annotations, and it does not")
		line("name the people who worked on the project. The story's own text may still mention real people.")
	}
	line("")
	line("## What it holds")
	line("")
	if m.Source.Kind == "release" {
		line("The content is release %s, whose files are under `release/`, byte for byte as its build stored", m.Source.Version)
		line("them. A release holds only what a game needs, so this archive has **no folders, no descriptions of")
		line("components, and none of the other data that only the draft holds**.")
		if m.Profile == ProfileFull {
			line("")
			line("Comments and play states are those of the project when the archive was made, about what this")
			line("release still has. %d comment(s) and %d play state(s) about anything else were left out.",
				m.LeftOut.Comments, m.LeftOut.PlayStates)
			line("What is in the project's trash is neither held nor counted, as a thread about something that the")
			line("draft has deleted since, even where this release still has it.")
		}
	} else {
		line("The content is the project's draft as it was when the archive was made, under `draft/`, without")
		line("what is in the project's trash.")
	}
	if m.Profile == ProfileFull {
		line("")
		line("A play state is held as it was saved, so its values and their names may still name a node, a")
		line("variable, or an attribute that has gone to the trash since. Loading it drops those and says so.")
	}
	line("")
	for _, s := range m.Sections {
		line("- `%s`: %s, %d item(s)", s.Path, s.Name, s.Items)
	}
	if m.IncludesReleases {
		line("")
		line("Each release of the history has its files under `releases/<its ID>/`.")
	}
	if m.IncludesAssets {
		line("")
		line("The files of the project's assets, node cover images among them, are under `assets/`.")
	} else {
		line("")
		line("This archive holds no assets: the images that its content refers to are not in it.")
	}
	return b.String()
}
