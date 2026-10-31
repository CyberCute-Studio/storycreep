-- Project search (package search, PRD FR-PROJ-03, plan note N57): a document
-- of each node, connection, variable, and component of a project, in the
-- trash or not, which a search reads of the entities that are not. A
-- batch's transaction writes the documents of the entities that it changed,
-- once every command of the batch has applied, under the lock of the
-- project's row (NodeSearchSources and the other kinds', which read each
-- entity's document with it, its title, the SHA-256 of its body, as
-- lowercase hexadecimal of the body's UTF-8, rather than the body itself,
-- and its version, and UpsertSearchEntries, for the documents that differ),
-- and the job of the index those of the entities that have none of the
-- current version, under the same lock (ProjectsToIndex,
-- LockProjectToIndex, UnindexedSearchEntities, SetSearchState). search_entry
-- and search_state are tenant tables, which a transaction of their workspace
-- reads.

-- name: NodeSearchSources :many
-- NodeSearchSources returns what the documents of the project's nodes whose
-- IDs are among ids are made of (search.Source), in the trash or not, their
-- titles and their texts, which a branch and a jumper have none of, with
-- each node's document as the index holds it, or none.
SELECT n.id, n.title, n.text AS doc, e.title AS entry_title,
    coalesce(encode(sha256(convert_to(e.body, 'UTF8')), 'hex'), '')::text AS entry_body_sha256,
    e.version AS entry_version
FROM node n
LEFT JOIN search_entry e ON e.workspace_id = n.workspace_id AND e.project_id = n.project_id AND e.kind = 'node'
    AND e.entity_id = n.id
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@ids::uuid[]);

-- name: ConnectionSearchSources :many
-- ConnectionSearchSources returns what the documents of the project's
-- connections whose IDs are among ids are made of, in the trash or not,
-- their labels, with each connection's document as the index holds it, or
-- none.
SELECT c.id, c.label AS doc, e.title AS entry_title,
    coalesce(encode(sha256(convert_to(e.body, 'UTF8')), 'hex'), '')::text AS entry_body_sha256,
    e.version AS entry_version
FROM connection c
LEFT JOIN search_entry e ON e.workspace_id = c.workspace_id AND e.project_id = c.project_id
    AND e.kind = 'connection' AND e.entity_id = c.id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@ids::uuid[]);

-- name: VariableSearchSources :many
-- VariableSearchSources returns what the documents of the project's
-- variables whose IDs are among ids are made of, in the trash or not, their
-- keys and their descriptions, with each variable's document as the index
-- holds it, or none.
SELECT v.id, v.key::text AS key, v.description, e.title AS entry_title,
    coalesce(encode(sha256(convert_to(e.body, 'UTF8')), 'hex'), '')::text AS entry_body_sha256,
    e.version AS entry_version
FROM variable v
LEFT JOIN search_entry e ON e.workspace_id = v.workspace_id AND e.project_id = v.project_id
    AND e.kind = 'variable' AND e.entity_id = v.id
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.id = ANY(@ids::uuid[]);

-- name: ComponentSearchSources :many
-- ComponentSearchSources returns what the documents of the project's
-- components whose IDs are among ids, or of a type that a definition among
-- attribute_ids is of, are made of, in the trash or not: their names, their
-- keys, their descriptions, and their values of the definitions of text that
-- are not deleted themselves, as strings, in the order of the definitions'
-- IDs, those of the types string, text, and enum, whatever becomes of their
-- types' place in the trash, which only a type whose components are all
-- there has; with each component's document as the index holds it, or none.
SELECT m.id, m.name, m.key::text AS key, m.description AS doc,
    ARRAY(
        SELECT a.value #>> '{}'
        FROM attribute_value a
        JOIN attribute_def d ON d.workspace_id = a.workspace_id AND d.project_id = a.project_id
            AND d.id = a.attribute_def_id
        WHERE a.workspace_id = m.workspace_id AND a.project_id = m.project_id AND a.component_id = m.id
            AND d.deleted_at IS NULL AND d.type IN ('string', 'text', 'enum') AND jsonb_typeof(a.value) = 'string'
        ORDER BY d.id
    )::text[] AS attribute_values,
    e.title AS entry_title,
    coalesce(encode(sha256(convert_to(e.body, 'UTF8')), 'hex'), '')::text AS entry_body_sha256,
    e.version AS entry_version
FROM component m
LEFT JOIN search_entry e ON e.workspace_id = m.workspace_id AND e.project_id = m.project_id
    AND e.kind = 'component' AND e.entity_id = m.id
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id AND (
    m.id = ANY(@ids::uuid[])
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
-- its kind, title, and body. It also deletes the project's state
-- (search_state) where a newer version of the server recorded it: an older
-- version's replica, which a rolling upgrade replaces only after the newer
-- version's job has run, writes the documents of what its batches change of
-- its own, older version, and the newer job, which would pass over a
-- project of its state, then writes them again of its own.
WITH newer AS (
    DELETE FROM search_state
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND version > @version
)
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
-- after_id. A rank is ts_rank's of words, the OR of query's words, which
-- adds up how well each of them matches, a word of a title above one of a
-- body, rather than of query, whose AND ts_rank ranks by how near its words
-- are to each other alone, which would rank a document whose title holds one
-- of them and whose body holds another a hundred words on close to nothing.
-- Each comes with its board, a node's, a connection's, or a variable's of a
-- board, with the board's name, its node, a node itself or the node that a
-- connection leaves, with the node's title, and its title and its body as
-- ts_headline marks the matches of words in them, between the characters
-- U+0002 and U+0003, which no document holds (search.Document): the whole
-- title, and of the body, from a match of one of the words on, up to 35
-- words, or, where fewer than 15 are left from it, up to 35 that end the
-- body, or else, where it holds none of them, its first 15 words, so that a
-- body of at most 15 words is whole. The kinds are words of lowercase ASCII
-- letters, which every collation orders alike, as Go does. The kinds'
-- conditions on the parameter are evaluated once, so that a search of one
-- kind reads the documents of no other.
WITH hits AS (
    SELECT e.kind, e.entity_id, n.board_id, n.id AS node_id, ts_rank(e.vector, @words::tsquery) AS rank
    FROM search_entry e
    JOIN live_node n ON n.workspace_id = e.workspace_id AND n.project_id = e.project_id AND n.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'node'
        AND @kind::text IN ('', 'node') AND e.vector @@ @query::tsquery
    UNION ALL
    SELECT e.kind, e.entity_id, c.board_id, c.from_node_id, ts_rank(e.vector, @words::tsquery)
    FROM search_entry e
    JOIN live_connection c ON c.workspace_id = e.workspace_id AND c.project_id = e.project_id
        AND c.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'connection'
        AND @kind::text IN ('', 'connection') AND e.vector @@ @query::tsquery
    UNION ALL
    SELECT e.kind, e.entity_id, v.board_id, NULL::uuid, ts_rank(e.vector, @words::tsquery)
    FROM search_entry e
    JOIN live_variable v ON v.workspace_id = e.workspace_id AND v.project_id = e.project_id AND v.id = e.entity_id
    WHERE e.workspace_id = @workspace_id AND e.project_id = @project_id AND e.kind = 'variable'
        AND @kind::text IN ('', 'variable') AND e.vector @@ @query::tsquery
    UNION ALL
    SELECT e.kind, e.entity_id, NULL::uuid, NULL::uuid, ts_rank(e.vector, @words::tsquery)
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
    ts_headline('public.storycreep_search'::regconfig, e.title, @words::tsquery,
        'HighlightAll=true, StartSel=' || chr(2) || ', StopSel=' || chr(3))::text AS title,
    ts_headline('public.storycreep_search'::regconfig, e.body, @words::tsquery,
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
