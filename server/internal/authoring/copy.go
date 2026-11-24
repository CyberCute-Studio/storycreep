package authoring

import (
	"context"
	"errors"
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/sample"
)

// DuplicateRefusedError is Duplicate's error for a project that the import of
// its copy refuses (plan note N92): one that holds more than an archive that
// an import takes, as more assets than MaxImportAssets, or a stored value
// that the import's checks of its rows refuse, or an asset whose file the
// store no longer has. Failure says what was refused, as an import's failure
// does.
type DuplicateRefusedError struct {
	Failure *ImportError
}

func (e *DuplicateRefusedError) Error() string {
	return "authoring: the copy of the project is refused: " + e.Failure.Error()
}

// Duplicate copies the project projectID into a new project of its workspace,
// named name, which the actor asks for, and returns the copy (PRD FR-PROJ-01,
// plan note N92). The copy is an import of the project's content-only archive
// with its assets, with fresh IDs (§5.2), so that it holds the project's
// draft as an export holds it: its folders, boards, variables, component
// types and components, nodes, connections, and muted findings, and its
// assets, and not its comments, play states, or releases. Its slug is derived
// from its name, as a new project's is, and its duplicated_from names the
// project. It is all or nothing: the transaction that creates the copy writes
// all of it, and its audit entry.
//
// It returns the errors of project.Service.CheckDuplicate, input.FieldErrors
// for a name that a new project may not take, asset.ErrSetupRequired while
// first-run setup may yet choose the store, project.ErrNotFound for a project
// that went to the trash meanwhile, the *entitlements.LimitError of a limit
// that the copy would pass, as its storage, and a *DuplicateRefusedError.
func (s *Service) Duplicate(ctx context.Context, actor commands.Actor, projectID ids.UUID, name string) (
	project.Visible, error) {
	source, err := s.projects.CheckDuplicate(ctx, actor, projectID)
	if err != nil {
		return project.Visible{}, err
	}
	if err := project.CheckNewName(name); err != nil {
		return project.Visible{}, err
	}
	ws, err := ids.Decode(ids.Workspace, source.WorkspaceID)
	if err != nil {
		return project.Visible{}, err
	}
	// The final store, as the copy's assets are stored as an upload's are.
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return project.Visible{}, err
	}
	// What an export of the draft, content-only, with its assets, and without
	// the releases, would hold.
	row := queries.AuthoringExport{WorkspaceID: uuid(ws), ProjectID: uuid(projectID), Profile: string(ProfileContent),
		IncludeAssets: true}
	c, err := s.read(ctx, row)
	if errors.Is(err, errProjectGone) {
		return project.Visible{}, project.ErrNotFound
	}
	if err != nil {
		return project.Visible{}, err
	}
	id := ids.New()
	w := &chunkWriter{ctx: ctx, store: store, ws: ws, id: id, buf: make([]byte, 0, ChunkBytes)}
	// Whatever happens, as nothing refers to the chunks once the copy has
	// been made, or not. One that a process that died left behind is the
	// orphan reconciliation's.
	defer func() { s.dropChunks(context.WithoutCancel(ctx), w.keys) }()
	if err := s.write(ctx, w, store, row, c); err != nil {
		if errors.Is(err, errAssetMissing) {
			return project.Visible{}, &DuplicateRefusedError{Failure: &ImportError{Code: FailAssetInvalid,
				Section: "assets", Check: "missing_file", Detail: err.Error()}}
		}
		return project.Visible{}, err
	}
	if err := w.flush(); err != nil {
		return project.Visible{}, err
	}
	var created project.Visible
	run := importRun{workspace: ws, importID: id, user: actor.UserID, remap: true, client: actor,
		internal: &internalRun{name: name, duplicatedFrom: projectID, action: audit.ActionProjectDuplicate,
			metadata: map[string]any{"duplicated_from": ids.Encode(ids.Project, projectID)}, created: &created}}
	r := newChunkReader(ctx, store, w.keys, ChunkBytes, w.size)
	if err := s.importArchive(ctx, run, r, w.size); err != nil {
		var refused *ImportError
		if errors.As(err, &refused) && refused.cause == nil {
			return project.Visible{}, &DuplicateRefusedError{Failure: refused.recorded()}
		}
		return project.Visible{}, internalError(err)
	}
	return created, nil
}

// ImportSample imports the sample project, The Lighthouse Keeper (PRD
// FR-PROJ-05, package sample), into the workspace, with fresh IDs, as the
// actor asks for, and returns it. It returns the errors of
// project.Service.CheckCreate, and the *entitlements.LimitError or the
// error of a role that the transaction that writes the project was refused
// for. An archive of the server's own that its import refuses is an error of
// the server's.
func (s *Service) ImportSample(ctx context.Context, actor commands.Actor, workspaceID ids.UUID) (project.Visible,
	error) {
	if err := s.projects.CheckCreate(ctx, actor.UserID, workspaceID); err != nil {
		return project.Visible{}, err
	}
	var created project.Visible
	run := importRun{workspace: workspaceID, importID: ids.New(), user: actor.UserID, remap: true, client: actor,
		internal: &internalRun{action: audit.ActionProjectImport,
			metadata: map[string]any{"sample": sample.Name, "id_policy": PolicyRemap}, created: &created}}
	r := sample.Reader()
	if err := s.importArchive(ctx, run, r, r.Size()); err != nil {
		var refused *ImportError
		if errors.As(err, &refused) && refused.cause == nil {
			return project.Visible{}, fmt.Errorf("authoring: the sample project is refused: %w", err)
		}
		return project.Visible{}, internalError(err)
	}
	return created, nil
}

// internalError returns err, an error of importArchive of an archive of the
// server's own, as its request is answered: the limit or the role that the
// transaction that writes the project was refused for, or err itself.
func internalError(err error) error {
	var refused *ImportError
	if errors.As(err, &refused) && refused.cause != nil {
		return refused.cause
	}
	return err
}

// chunkWriter writes an archive into blobs of the store of ChunkBytes each but
// the last, under keys of a chunk of an import (chunkKey), as an upload's
// chunks are, which newChunkReader reads, so that a copy holds at most a chunk
// of its archive in memory while it is written, and another while it is read.
type chunkWriter struct {
	ctx   context.Context
	store blob.Store
	ws    ids.UUID
	id    ids.UUID
	buf   []byte
	keys  []string
	size  int64
}

// Write implements io.Writer.
func (w *chunkWriter) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		take := min(len(p), ChunkBytes-len(w.buf))
		w.buf = append(w.buf, p[:take]...)
		p, n = p[take:], n+take
		if len(w.buf) == ChunkBytes {
			if err := w.flush(); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// flush stores what the buffer holds as the next chunk, if it holds anything.
func (w *chunkWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	key := chunkKey(w.ws, w.id, len(w.keys))
	// Recorded before the store is asked, so that a chunk that the store may
	// have kept from a failed request is deleted with the rest.
	w.keys = append(w.keys, key)
	if err := w.store.Put(w.ctx, key, w.buf, "application/octet-stream"); err != nil {
		return fmt.Errorf("authoring: store a chunk of the copy's archive: %w", err)
	}
	w.size += int64(len(w.buf))
	w.buf = w.buf[:0]
	return nil
}
