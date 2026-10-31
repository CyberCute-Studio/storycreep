-- Project search (package search, PRD FR-PROJ-03, plan note N57): a document
-- of each node, connection, variable, and component of a project, in the
-- trash or not, which a search reads of the entities that are not. A
-- batch's transaction writes the documents of the entities that it changed,
-- once every command of the batch has applied, under the lock of the
-- project's row (SearchSources, UpsertSearchEntries), and the job of the
-- index those of the entities that have none of the current version, under
-- the same lock (ProjectsToIndex, LockProjectToIndex,
-- UnindexedSearchEntities, SetSearchState). search_entry and search_state
-- are tenant tables, which a transaction of their workspace reads.

-- name: SearchSources :many
-- SearchSources returns what the documents of the project's entities are
-- made of (search.Source), in the trash or not: of each node whose ID is
-- among node_ids, its title and its text, which a branch and a jumper have
-- none of; of each connection among connection_ids, its label; of each
-- variable among variable_ids, its key and its description; and of each
-- component among component_ids, or of a type that a definition among
-- attribute_ids is of, its name, its key, its description, and its values of
-- the definitions of text that are not deleted themselves, as strings, in
-- the order of the definitions' IDs: those of the types string, text, and
-- enum, whatever becomes of their types' place in the trash, which only a
-- type whose components are all there has.
SELECT 'node'::text AS kind, n.id, n.title, n.text AS doc, ''::text AS key, ''::text AS description,
    '{}'::text[] AS attribute_values
FROM node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@node_ids::uuid[])
UNION ALL
SELECT 'connection', c.id, '', c.label, '', '', '{}'
FROM connection c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@connection_ids::uuid[])
UNION ALL
SELECT 'variable', v.id, v.key COLLATE "default", NULL, '', v.description, '{}'
FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.id = ANY(@variable_ids::uuid[])
UNION ALL
SELECT 'component', m.id, m.name, m.description, m.key COLLATE "default", '',
    ARRAY(
        SELECT a.value #>> '{}'
        FROM attribute_value a
        JOIN attribute_def d ON d.workspace_id = a.workspace_id AND d.project_id = a.project_id
            AND d.id = a.attribute_def_id
        WHERE a.workspace_id = m.workspace_id AND a.project_id = m.project_id AND a.component_id = m.id
            AND d.deleted_at IS NULL AND d.type IN ('string', 'text', 'enum') AND jsonb_typeof(a.value) = 'string'
        ORDER BY d.id
    )
FROM component m
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id AND (
    m.id = ANY(@component_ids::uuid[])
    OR m.component_type_id IN (
        SELECT d.component_type_id FROM attribute_def d
        WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id AND d.id = ANY(@attribute_ids::uuid[])
    )
);

-- name: UpsertSearchEntries :exec
-- UpsertSearchEntries writes the documents of the project's entities that
-- kinds, entity_ids, titles, and bodies list, one of each for each, of the
-- version, but leaves as it is a document that holds them already, so that
-- an edit that changes nothing that a document holds, as an edit of a
-- node's script, rewrites none. The arrays are of one length, so their
-- unnest calls, which PostgreSQL runs side by side, pair each entity with
-- its kind, title, and body.
INSERT INTO search_entry (workspace_id, project_id, kind, entity_id, title, body, version)
SELECT @workspace_id, @project_id, d.kind, d.entity_id, d.title, d.body, @version
FROM (SELECT unnest(@kinds::text[]) AS kind, unnest(@entity_ids::uuid[]) AS entity_id, unnest(@titles::text[]) AS title,
    unnest(@bodies::text[]) AS body) AS d
ON CONFLICT (workspace_id, project_id, kind, entity_id) DO UPDATE
SET title = excluded.title, body = excluded.body, version = excluded.version
WHERE (search_entry.title, search_entry.body, search_entry.version)
    IS DISTINCT FROM (excluded.title, excluded.body, excluded.version);

-- name: SearchLexemes :one
-- SearchLexemes returns the words of query as the search configuration reads
-- them, lowercased and without their diacritics, each once, in no order that
-- matters, as a search looks up each of them.
SELECT tsvector_to_array(to_tsvector('public.storycreep_search'::regconfig, @query::text))::text[] AS lexemes;

-- name: Search :many
-- Search returns at most max_rows of the documents of the project's
-- entities that are not in its trash that query matches, of the kind, or of
-- every kind for the empty kind, in the order of their ranks, highest first,
-- their kinds, and their entities' UUIDs: the first, unless after is set, or
-- else those after the rank after_rank, the kind after_kind, and the UUID
-- after_id. Each comes with its board, a node's, a connection's, or a
-- variable's of a board, with the board's name, its node, a node itself or
-- the node that a connection leaves, with the node's title, and its title
-- and its body as ts_headline marks query's matches in them, between the
-- characters U+0002 and U+0003, which no document holds (search.Document):
-- the whole title, and the body whole where it has at most 35 words, or else
-- 15 to 35 of its words from the start of its best match, or from its start
-- where it has none. The kinds are words of lowercase ASCII letters,
-- which every collation orders alike, as Go does. The kinds' conditions on
-- the parameter are evaluated once, so that a search of one kind reads the
-- documents of no other.
WITH hits AS (
    SELECT e.kind, e.entity_id, n.board_id, n.id AS node_id, ts_rank(e.vector, @query::tsquery) AS rank
    FROM search_entry e
    JOIN live_node n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'node'
        AND @kind::text IN ('', 'node') AND e.vector @@ @query::tsquery
    UNION ALL
    SELECT e.kind, e.entity_id, c.board_id, c.from_node_id, ts_rank(e.vector, @query::tsquery)
    FROM search_entry e
    JOIN live_connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id
        AND c.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'connection'
        AND @kind::text IN ('', 'connection') AND e.vector @@ @query::tsquery
    UNION ALL
    SELECT e.kind, e.entity_id, v.board_id, NULL::uuid, ts_rank(e.vector, @query::tsquery)
    FROM search_entry e
    JOIN live_variable v ON v.workspace_id = e.workspace_id AND v.project_id = e.project_id AND v.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'variable'
        AND @kind::text IN ('', 'variable') AND e.vector @@ @query::tsquery
    UNION ALL
    SELECT e.kind, e.entity_id, NULL::uuid, NULL::uuid, ts_rank(e.vector, @query::tsquery)
    FROM search_entry e
    JOIN live_component m ON m.workspace_id = e.workspace_id AND m.project_id = e.project_id
        AND m.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'component'
        AND @kind::text IN ('', 'component') AND e.vector @@ @query::tsquery
), page AS (
    SELECT h.kind, h.entity_id, h.board_id, h.node_id, h.rank
    FROM hits h
    WHERE NOT @after::boolean OR h.rank < @after_rank::real OR h.rank = @after_rank::real
        AND (h.kind > @after_kind::text OR h.kind = @after_kind::text AND h.entity_id > @after_id::uuid)
    ORDER BY h.rank DESC, h.kind, h.entity_id
    LIMIT @max_rows
)
SELECT p.kind, p.entity_id, p.board_id, b.name AS board_name, p.node_id, nd.title AS node_title, p.rank,
    ts_headline('public.storycreep_search'::regconfig, e.title, @query::tsquery,
        'HighlightAll=true, StartSel=' || chr(2) || ', StopSel=' || chr(3))::text AS title,
    ts_headline('public.storycreep_search'::regconfig, e.body, @query::tsquery,
        'MaxWords=35, MinWords=15, StartSel=' || chr(2) || ', StopSel=' || chr(3))::text AS snippet
FROM page p
JOIN search_entry e ON e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = p.kind
    AND e.entity_id = p.entity_id
LEFT JOIN board b ON b.workspace_id = @workspace_id AND b.project_id = @project_id AND b.id = p.board_id
LEFT JOIN node nd ON nd.workspace_id = @workspace_id AND nd.project_id = @project_id AND nd.id = p.node_id
ORDER BY p.rank DESC, p.kind, p.entity_id;

-- name: ProjectsToIndex :many
-- ProjectsToIndex returns the workspace's projects whose documents the job
-- of the index has not written whole of the version, in the order of their
-- IDs.
SELECT p.id FROM project p
WHERE p.workspace_id = @workspace_id AND NOT EXISTS (
    SELECT 1 FROM search_state s WHERE s.workspace_id = p.workspace_id AND s.project_id = p.id
        AND s.version = @version
)
ORDER BY p.id;

-- name: LockProjectToIndex :one
-- LockProjectToIndex locks the project's row as a batch of its commands
-- does (LockProjectSeq), so that the job of the index writes no document
-- that a batch changes the entity of meanwhile, and returns its ID, or no row
-- if the workspace has no such project, its time in the workspace's trash has
-- passed, which the purge of projects deletes it for, or a transaction holds
-- the lock, as a batch does while it applies, which a later run of the job
-- waits out.
SELECT id FROM project
WHERE workspace_id = @workspace_id AND id = @id AND (hard_delete_after IS NULL OR hard_delete_after > now())
FOR NO KEY UPDATE SKIP LOCKED;

-- name: UnindexedSearchEntities :many
-- UnindexedSearchEntities returns at most max_rows of the project's nodes,
-- connections, variables, and components, in the trash or not, that have no
-- document of the version, each with its kind.
SELECT 'node'::text AS kind, n.id
FROM node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND NOT EXISTS (
    SELECT 1 FROM search_entry e WHERE e.workspace_id = n.workspace_id AND e.project_id = n.project_id
        AND e.kind = 'node' AND e.entity_id = n.id AND e.version = @version
)
UNION ALL
SELECT 'connection', c.id
FROM connection c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND NOT EXISTS (
    SELECT 1 FROM search_entry e WHERE e.workspace_id = c.workspace_id AND e.project_id = c.project_id
        AND e.kind = 'connection' AND e.entity_id = c.id AND e.version = @version
)
UNION ALL
SELECT 'variable', v.id
FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND NOT EXISTS (
    SELECT 1 FROM search_entry e WHERE e.workspace_id = v.workspace_id AND e.project_id = v.project_id
        AND e.kind = 'variable' AND e.entity_id = v.id AND e.version = @version
)
UNION ALL
SELECT 'component', m.id
FROM component m
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id AND NOT EXISTS (
    SELECT 1 FROM search_entry e WHERE e.workspace_id = m.workspace_id AND e.project_id = m.project_id
        AND e.kind = 'component' AND e.entity_id = m.id AND e.version = @version
)
LIMIT @max_rows;

-- name: SetSearchState :exec
-- SetSearchState records that the job of the index has written the document
-- of every entity of the project of the version.
INSERT INTO search_state (workspace_id, project_id, version) VALUES (@workspace_id, @project_id, @version)
ON CONFLICT (workspace_id, project_id) DO UPDATE SET version = excluded.version;
