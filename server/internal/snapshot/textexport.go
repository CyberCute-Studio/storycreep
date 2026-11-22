package snapshot

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics"
	"github.com/CyberCute-Studio/storycreep/server/internal/textexport"
)

// The text export of a snapshot or of the draft (PRD FR-TEXT-05, plan item
// 82, note N89).

// TextExport returns the text export of the ready snapshot snapshotID of the
// project projectID, of the board of the public ID board, or of the whole
// project for "", read at the version of the published format that the server
// writes, as Diff reads a snapshot (render), once the actor may read the
// project. It returns the errors of project.Service.Get, ErrNotFound for a
// snapshot or a board that the project's snapshot does not have, a
// *NotReadyError for one that is building or failed, and an
// *schemaversion.UnrepresentableError for one stored at a newer version whose
// content the server's cannot express. The token's project:read is the
// route's to check.
func (s *Service) TextExport(ctx context.Context, actor commands.Actor, projectID, snapshotID ids.UUID, board string) (
	*textexport.Export, error) {
	var row queries.SnapshotRow
	var files schemaversion.Files
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		row, files, err = readyFiles(ctx, tx, ws, projectID, snapshotID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if files, _, err = render(files, int(row.SchemaVersion.Int32), published.SchemaVersion); err != nil {
		return nil, err
	}
	e, err := textexport.FromFiles(files, board)
	if err != nil {
		return nil, textExportError(err)
	}
	e.Source = textexport.Source{Kind: textexport.SourceSnapshot, ID: ids.Encode(ids.Snapshot, snapshotID),
		Version: row.Version}
	return e, nil
}

// TextExportDraft returns the text export of the draft of the project
// projectID as it is now, of the board of the public ID board, or of the
// whole project for "", read in one transaction as of one moment, with the
// names of what its texts name as the writing statistics read them, in the
// trash or not, so that its word counts add up to theirs (FR-TEXT-05 AC3). It
// returns the errors of project.Service.Get, and ErrNotFound for a board that
// the draft does not hold, or that is in its trash. The token's project:read
// is the route's to check.
func (s *Service) TextExportDraft(ctx context.Context, actor commands.Actor, projectID ids.UUID, board string) (
	*textexport.Export, error) {
	var draft *serializer.Draft
	var names statistics.Names
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		if draft, err = serializer.Load(ctx, tx, ws, projectID); err != nil {
			return err
		}
		docs := make([]*richtext.Doc, 0, len(draft.Nodes)+len(draft.Connections))
		for i := range draft.Nodes {
			docs = append(docs, draft.Nodes[i].Text)
		}
		for i := range draft.Connections {
			docs = append(docs, draft.Connections[i].Label)
		}
		names, err = statistics.ReadNames(ctx, queries.New(tx), ws, projectID, docs)
		return err
	})
	if err != nil {
		return nil, err
	}
	e, err := textexport.FromDraft(draft, names, board)
	if err != nil {
		return nil, textExportError(err)
	}
	return e, nil
}

// textExportError returns ErrNotFound for a board that the export's source
// does not hold, which the API answers as it answers a snapshot that the
// project does not have, or err.
func textExportError(err error) error {
	if errors.Is(err, textexport.ErrBoardNotFound) {
		return ErrNotFound
	}
	return err
}
