// Package statisticstest checks the writing statistics (package statistics,
// PRD FR-TEXT-06) against a recount of them from scratch: of what every node
// and connection of a project holds, as it is stored, with the names that the
// project has now, rather than from what the batches that wrote them changed,
// which is how the pipeline keeps the statistics.
package statisticstest

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/statistics"
)

// Drift returns how the rows of the statistics of the project differ from
// what a recount of its texts from scratch gives (statistics.Count), each
// row that the statistics lack, or hold otherwise, as "lacks …", and each
// that they have beyond them, as "has …", or nothing if they are the same: a
// row of the current version of each node and connection of the project, in
// the trash or not. conn is the superuser's connection, which sees every
// workspace's rows.
func Drift(ctx context.Context, conn queries.DBTX, project ids.UUID) ([]string, error) {
	var workspace pgtype.UUID
	if err := conn.QueryRow(ctx, "SELECT workspace_id FROM project WHERE id = $1", project).Scan(
		&workspace); err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, `SELECT 'node', id, text FROM node WHERE project_id = $1
		UNION ALL
		SELECT 'connection', id, label FROM connection WHERE project_id = $1`, project)
	if err != nil {
		return nil, err
	}
	type source struct {
		Kind string
		ID   pgtype.UUID
		Doc  []byte
	}
	sources, err := pgx.CollectRows(rows, pgx.RowToStructByPos[source])
	if err != nil {
		return nil, err
	}
	docs := make([]*richtext.Doc, len(sources))
	for i, s := range sources {
		if s.Doc == nil {
			continue
		}
		if docs[i], err = richtext.Parse(s.Doc); err != nil {
			return nil, fmt.Errorf("the text of the %s %s: %w", s.Kind, ids.UUID(s.ID.Bytes), err)
		}
	}
	names, err := statistics.ReadNames(ctx, queries.New(conn), workspace.Bytes, project, docs)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for i, s := range sources {
		words, characters := statistics.Count(docs[i], names)
		want[line(s.Kind, s.ID, words, characters, statistics.Version)] = true
	}
	rows, err = conn.Query(ctx, `SELECT kind, entity_id, words, characters, version FROM text_statistic
		WHERE project_id = $1`, project)
	if err != nil {
		return nil, err
	}
	type entry struct {
		Kind       string
		ID         pgtype.UUID
		Words      int32
		Characters int32
		Version    int32
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[line(e.Kind, e.ID, int(e.Words), int(e.Characters), int(e.Version))] = true
	}
	var drift []string
	for _, l := range slices.Sorted(maps.Keys(want)) {
		if !have[l] {
			drift = append(drift, "lacks "+l)
		}
	}
	for _, l := range slices.Sorted(maps.Keys(have)) {
		if !want[l] {
			drift = append(drift, "has "+l)
		}
	}
	return drift, nil
}

// line describes a row of the statistics.
func line(kind string, id pgtype.UUID, words, characters, version int) string {
	return fmt.Sprintf("%s %s v%d: %d words, %d characters", kind, ids.UUID(id.Bytes), version, words, characters)
}
