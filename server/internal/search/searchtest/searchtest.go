// Package searchtest checks the index of project search (package search, PRD
// FR-PROJ-03) against a recomputation of it from scratch: from what every
// node, connection, variable, and component of a project holds, as it is
// stored, rather than from what the batches that wrote them changed, which
// is how the pipeline keeps the index.
package searchtest

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
)

// Querier runs a query: the superuser's connection, which sees every
// workspace's rows, or a transaction of the project's workspace.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Drift returns how the documents of the project differ from what a
// recomputation of them from scratch gives (search.NewDocument), each
// document that the index lacks, or holds otherwise, as "lacks …", and each
// that it has beyond them, as "has …", or nothing if they are the same: a
// document of the current version of each node, connection, variable, and
// component of the project, in the trash or not.
func Drift(ctx context.Context, conn Querier, project ids.UUID) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT 'node', id, title, text, '', '', '{}'::text[] FROM node
			WHERE project_id = $1
		UNION ALL
		SELECT 'connection', id, '', label, '', '', '{}' FROM connection WHERE project_id = $1
		UNION ALL
		SELECT 'variable', id, key COLLATE "default", NULL, '', description, '{}' FROM variable
			WHERE project_id = $1
		UNION ALL
		SELECT 'component', m.id, m.name, m.description, m.key COLLATE "default", '', ARRAY(
			SELECT a.value #>> '{}' FROM attribute_value a JOIN attribute_def d ON d.id = a.attribute_def_id
			WHERE a.component_id = m.id AND d.deleted_at IS NULL AND d.type IN ('string', 'text', 'enum')
				AND jsonb_typeof(a.value) = 'string'
			ORDER BY d.id)
		FROM component m WHERE m.project_id = $1`, project)
	if err != nil {
		return nil, err
	}
	type source struct {
		Kind        string
		ID          pgtype.UUID
		Title       string
		Doc         []byte
		Key         string
		Description string
		Values      []string
	}
	sources, err := pgx.CollectRows(rows, pgx.RowToStructByPos[source])
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, s := range sources {
		d, err := search.NewDocument(search.Source{Kind: search.Kind(s.Kind), ID: s.ID.Bytes, Title: s.Title,
			Doc: s.Doc, Key: s.Key, Description: s.Description, Values: s.Values})
		if err != nil {
			return nil, err
		}
		want[line(string(d.Kind), s.ID, d.Title, d.Body, search.Version)] = true
	}
	rows, err = conn.Query(ctx, `SELECT kind, entity_id, title, body, version FROM search_entry
		WHERE project_id = $1`, project)
	if err != nil {
		return nil, err
	}
	type entry struct {
		Kind    string
		ID      pgtype.UUID
		Title   string
		Body    string
		Version int32
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[line(e.Kind, e.ID, e.Title, e.Body, int(e.Version))] = true
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

// line describes a document.
func line(kind string, id pgtype.UUID, title, body string, version int) string {
	return fmt.Sprintf("%s %s v%d: title %q, body %q", kind, ids.UUID(id.Bytes), version, title, body)
}
