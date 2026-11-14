-- The writing statistics (package statistics, PRD FR-TEXT-06, plan note N76):
-- the words and the characters of each node's text and each connection's
-- label of a project, in the trash or not, which a read adds up of the
-- entities that are not. A batch's transaction writes the rows of the texts
-- that it changed, and of those that name what it renamed, once every
-- command of the batch has applied, under the lock of the project's row
-- (NodeTextSources, ConnectionTextSources, which read each text with its row,
-- TextsNaming, TextsNamingDefinitions, TextNames, and UpsertTextStatistics,
-- for the rows that differ), and the job of the statistics those of the entities that have none
-- of the current version or a newer one, under the same lock
-- (ProjectsToCount, LockProjectToIndex, UncountedTexts, SetTextStatisticState),
-- as the trash's purge counts again the texts that name what it deleted
-- (TextsNamingMissing). text_statistic and text_statistic_state are tenant
-- tables, which a transaction of their workspace reads.

-- name: NodeTextSources :many
-- NodeTextSources returns the texts of the project's nodes whose IDs are
-- among ids, in the trash or not, which a branch and a jumper have none of,
-- each with its node's row of the statistics, or none.
SELECT n.id, n.text AS doc, s.words AS entry_words, s.characters AS entry_characters,
    s.version AS entry_version
FROM node n
LEFT JOIN text_statistic s ON s.workspace_id = n.workspace_id AND s.project_id = n.project_id AND s.kind = 'node'
    AND s.entity_id = n.id
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = ANY(@ids::uuid[]);

-- name: ConnectionTextSources :many
-- ConnectionTextSources returns the labels of the project's connections whose
-- IDs are among ids, in the trash or not, each with its connection's row of
-- the statistics, or none.
SELECT c.id, c.label AS doc, s.words AS entry_words, s.characters AS entry_characters,
    s.version AS entry_version
FROM connection c
LEFT JOIN text_statistic s ON s.workspace_id = c.workspace_id AND s.project_id = c.project_id
    AND s.kind = 'connection' AND s.entity_id = c.id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@ids::uuid[]);

-- name: TextsNaming :many
-- TextsNaming returns the nodes and the connections of the project, in the
-- trash or not, whose texts name, by the index of what they name (package
-- dependency), a variable among variable_ids, by a value reference, a
-- component among named_ids, by a mention, which shows its name, or a
-- component among keyed_ids, by a value reference of one of its attributes,
-- which shows its key, each once: the texts whose counts a change of those
-- names changes. The index of the targets finds each of its arms, so that
-- it reads the rows that name what it is given alone. The texts that name a
-- definition TextsNamingDefinitions returns, by the index of the
-- definitions, which its caller runs only for a batch that changed one, so
-- that a batch that changed none reads that index not at all.
SELECT DISTINCT r.node_id, r.connection_id
FROM expression_ref r
WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.expression_id IS NULL
    AND r.component_id IS NULL AND (
        r.target_kind = 'variable' AND r.target_id = ANY(@variable_ids::uuid[])
        OR r.target_kind = 'component' AND r.target_id = ANY(@named_ids::uuid[])
        OR r.target_kind = 'attribute' AND r.target_id = ANY(@keyed_ids::uuid[])
    );

-- name: TextsNamingDefinitions :many
-- TextsNamingDefinitions returns the nodes and the connections of the
-- project, in the trash or not, whose texts name, by the index of what they
-- name, a definition among attribute_ids, by a value reference of the
-- attribute of a component, which shows its key, each once. A value
-- reference names its component and its definition whether the definition
-- is of the component's type or not, as one pasted from another project
-- may, which the validation reports, and shows both keys all the same, so a
-- definition's texts are found by the definition alone, by the index of the
-- definitions (expression_ref_attribute_def, migration 00030), which reads
-- the rows that name what it is given alone: its caller runs it only for a
-- batch that changed a definition's key, or created one (TextsNaming).
SELECT DISTINCT r.node_id, r.connection_id
FROM expression_ref r
WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.expression_id IS NULL
    AND r.component_id IS NULL AND r.target_kind = 'attribute' AND r.attribute_def_id = ANY(@attribute_ids::uuid[]);

-- name: TextsNamingMissing :many
-- TextsNamingMissing returns the nodes and the connections of the project,
-- in the trash or not, whose texts name, by the index of what they name
-- (package dependency), a variable, a component, or an attribute's
-- definition that the project no longer has, as once the trash's purge has
-- deleted it, each once: the texts whose counts the names that they lost
-- change.
SELECT DISTINCT r.node_id, r.connection_id
FROM expression_ref r
WHERE r.workspace_id = @workspace_id AND r.project_id = @project_id AND r.expression_id IS NULL
    AND r.component_id IS NULL AND (
        r.target_kind = 'variable' AND NOT EXISTS (
            SELECT 1 FROM variable v WHERE v.workspace_id = r.workspace_id AND v.project_id = r.project_id
                AND v.id = r.target_id
        )
        OR r.target_kind IN ('component', 'attribute') AND NOT EXISTS (
            SELECT 1 FROM component m WHERE m.workspace_id = r.workspace_id AND m.project_id = r.project_id
                AND m.id = r.target_id
        )
        OR r.target_kind = 'attribute' AND NOT EXISTS (
            SELECT 1 FROM attribute_def d WHERE d.workspace_id = r.workspace_id AND d.project_id = r.project_id
                AND d.id = r.attribute_def_id
        )
    );

-- name: TextNames :many
-- TextNames returns the names that the texts' counts read of the project's
-- variables whose IDs are among variable_ids, components among
-- component_ids, and attributes' definitions among attribute_ids, in the
-- trash or not, each with its kind: a variable's key, a component's name and
-- its key, and a definition's key. One that the project does not have, as
-- one that the trash's purge deleted, is not returned.
SELECT 'variable'::text AS kind, v.id, v.key::text AS name, ''::text AS key
FROM variable v
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.id = ANY(@variable_ids::uuid[])
UNION ALL
SELECT 'component', m.id, m.name, m.key::text
FROM component m
WHERE m.workspace_id = @workspace_id AND m.project_id = @project_id AND m.id = ANY(@component_ids::uuid[])
UNION ALL
SELECT 'attribute', d.id, d.key::text, ''
FROM attribute_def d
WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id AND d.id = ANY(@attribute_ids::uuid[]);

-- name: UpsertTextStatistics :exec
-- UpsertTextStatistics writes the rows of the project's texts that kinds,
-- entity_ids, words, and characters list, one of each for each, of the
-- version, over those that it holds of them. Its caller passes only the rows
-- that differ from those held, as it read them under the project's lock
-- (NodeTextSources, ConnectionTextSources), so that an edit that changes no
-- count, as an edit of a node's title, rewrites none. The arrays are of one
-- length, so their unnest calls, which PostgreSQL runs side by side, pair
-- each entity with its kind and counts. It also deletes the project's state
-- (text_statistic_state) where a newer version of the server recorded it:
-- an older version's replica, which a rolling upgrade replaces only after
-- the newer version's job has run, writes the rows of what its batches
-- change of its own, older version, and the newer job, which would pass over
-- a project of its state, then writes them again of its own.
WITH newer AS (
    DELETE FROM text_statistic_state
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND version > @version
)
INSERT INTO text_statistic (workspace_id, project_id, kind, entity_id, words, characters, version)
SELECT @workspace_id, @project_id, d.kind, d.entity_id, d.words, d.characters, @version
FROM (SELECT unnest(@kinds::text[]) AS kind, unnest(@entity_ids::uuid[]) AS entity_id,
    unnest(@words::integer[]) AS words, unnest(@characters::integer[]) AS characters) AS d
ON CONFLICT (workspace_id, project_id, kind, entity_id) DO UPDATE
SET words = excluded.words, characters = excluded.characters, version = excluded.version;

-- name: ProjectsToCount :many
-- ProjectsToCount returns the workspace's projects whose texts the job of
-- the statistics has not counted whole of the version, or of a newer one, in
-- the order of their IDs, as ProjectsToIndex returns those of the search's
-- index: an older version's job passes over the projects that a newer one
-- has counted, and a project whose time in the workspace's trash has passed,
-- which the purge of projects deletes, is left out.
SELECT p.id FROM project p
WHERE p.workspace_id = @workspace_id AND (p.hard_delete_after IS NULL OR p.hard_delete_after > now())
    AND NOT EXISTS (
        SELECT 1 FROM text_statistic_state s WHERE s.workspace_id = p.workspace_id AND s.project_id = p.id
            AND s.version >= @version
    )
ORDER BY p.id;

-- name: UncountedTexts :many
-- UncountedTexts returns at most max_rows of the project's nodes and
-- connections, in the trash or not, that have no row of the statistics of
-- the version, or of a newer one, which an older version's job leaves as it
-- is, each with its kind.
SELECT 'node'::text AS kind, n.id
FROM node n
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND NOT EXISTS (
    SELECT 1 FROM text_statistic s WHERE s.workspace_id = n.workspace_id AND s.project_id = n.project_id
        AND s.kind = 'node' AND s.entity_id = n.id AND s.version >= @version
)
UNION ALL
SELECT 'connection', c.id
FROM connection c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND NOT EXISTS (
    SELECT 1 FROM text_statistic s WHERE s.workspace_id = c.workspace_id AND s.project_id = c.project_id
        AND s.kind = 'connection' AND s.entity_id = c.id AND s.version >= @version
)
LIMIT @max_rows;

-- name: SetTextStatisticState :exec
-- SetTextStatisticState records that the job of the statistics has written
-- the row of every text of the project of the version, but keeps the state
-- of a newer version, as SetSearchState does.
INSERT INTO text_statistic_state (workspace_id, project_id, version) VALUES (@workspace_id, @project_id, @version)
ON CONFLICT (workspace_id, project_id) DO UPDATE
SET version = greatest(text_statistic_state.version, excluded.version);

-- name: BoardStatistics :many
-- BoardStatistics returns the statistics of each of the project's boards
-- that is not in its trash, in the order of their IDs, of what of it is not
-- in the trash: its name; how many nodes it has; the words and the
-- characters of their texts and of the labels of their connections, as
-- their rows of the statistics hold them; how many of the connections are
-- choices, those that leave an element that is not an ending, by its out
-- (PRD §5.8.5); how many conditions its connections and its branches'
-- outputs have, of those that are written, as an output's is not until it
-- is, its source being empty, while a connection's that is cleared is no
-- expression, one of white space alone being written, as one that does not
-- compile; and how many of the nodes and the connections have no row of
-- the version or of a newer one, whose texts the job of the statistics has
-- yet to count. Each kind is read once, grouped by board, each row with its
-- row of the statistics and its condition, of which it has one at most, by
-- the key of its expressions, so that the read reads no row twice, and in
-- time that grows with the project's size alone: about 40 ms on the large
-- fixture (BenchmarkStatistics).
WITH nodes AS (
    SELECT n.board_id, count(*) AS nodes, sum(s.words) AS words, sum(s.characters) AS characters,
        count(*) FILTER (WHERE s.version IS NULL OR s.version < @version) AS uncounted
    FROM live_node n
    LEFT JOIN text_statistic s ON s.workspace_id = n.workspace_id AND s.project_id = n.project_id
        AND s.kind = 'node' AND s.entity_id = n.id
    WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id
    GROUP BY n.board_id
), connections AS (
    SELECT c.board_id,
        count(*) FILTER (WHERE f.type = 'element' AND c.from_output_id IS NULL AND NOT f.is_ending) AS choices,
        sum(s.words) AS words, sum(s.characters) AS characters,
        count(*) FILTER (WHERE s.version IS NULL OR s.version < @version) AS uncounted,
        count(e.id) AS conditions
    FROM live_connection c
    JOIN node f ON f.workspace_id = c.workspace_id AND f.project_id = c.project_id AND f.id = c.from_node_id
    LEFT JOIN text_statistic s ON s.workspace_id = c.workspace_id AND s.project_id = c.project_id
        AND s.kind = 'connection' AND s.entity_id = c.id
    LEFT JOIN expression e ON e.workspace_id = c.workspace_id AND e.project_id = c.project_id
        AND e.connection_id = c.id AND e.field = 'condition'
    WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id
    GROUP BY c.board_id
), outputs AS (
    SELECT o.board_id, count(*) AS conditions
    FROM live_branch_output o
    JOIN expression e ON e.workspace_id = o.workspace_id AND e.project_id = o.project_id
        AND e.branch_output_id = o.id AND e.field = 'condition' AND e.source <> ''
    WHERE o.workspace_id = @workspace_id AND o.project_id = @project_id
    GROUP BY o.board_id
)
SELECT b.id, b.name,
    coalesce(n.nodes, 0)::bigint AS nodes,
    (coalesce(n.words, 0) + coalesce(c.words, 0))::bigint AS words,
    (coalesce(n.characters, 0) + coalesce(c.characters, 0))::bigint AS characters,
    coalesce(c.choices, 0)::bigint AS choices,
    (coalesce(c.conditions, 0) + coalesce(o.conditions, 0))::bigint AS conditions,
    (coalesce(n.uncounted, 0) + coalesce(c.uncounted, 0))::bigint AS uncounted
FROM live_board b
LEFT JOIN nodes n ON n.board_id = b.id
LEFT JOIN connections c ON c.board_id = b.id
LEFT JOIN outputs o ON o.board_id = b.id
WHERE b.workspace_id = @workspace_id AND b.project_id = @project_id
ORDER BY b.id;

-- name: NodeStatistics :one
-- NodeStatistics returns the statistics of the project's node id, if it is
-- not in the trash, of what of it is not: the words and the characters of
-- its text and of the labels of the connections that leave it, as their rows
-- of the statistics hold them; how many of those connections are choices,
-- which leave an element that is not an ending (BoardStatistics); how many
-- conditions they and the node's outputs have, of those that are written
-- (BoardStatistics); and how many of the node and the connections have no
-- row of the version or of a newer one. It reads the node's connections and
-- outputs by the indexes of their node, and their conditions by the keys of
-- their expressions, so that it reads what the node has alone. A node in the
-- trash, or that the project does not have, has no row.
SELECT n.id,
    (coalesce(s.words, 0) + coalesce(c.words, 0))::bigint AS words,
    (coalesce(s.characters, 0) + coalesce(c.characters, 0))::bigint AS characters,
    coalesce(c.choices, 0)::bigint AS choices,
    (coalesce(c.conditions, 0) + coalesce(o.conditions, 0))::bigint AS conditions,
    ((s.version IS NULL OR s.version < @version)::integer + coalesce(c.uncounted, 0))::bigint AS uncounted
FROM live_node n
LEFT JOIN text_statistic s ON s.workspace_id = n.workspace_id AND s.project_id = n.project_id AND s.kind = 'node'
    AND s.entity_id = n.id
LEFT JOIN LATERAL (
    SELECT count(*) FILTER (WHERE n.type = 'element' AND lc.from_output_id IS NULL AND NOT n.is_ending) AS choices,
        sum(cs.words) AS words, sum(cs.characters) AS characters,
        count(*) FILTER (WHERE cs.version IS NULL OR cs.version < @version) AS uncounted,
        count(e.id) AS conditions
    FROM live_connection lc
    LEFT JOIN text_statistic cs ON cs.workspace_id = lc.workspace_id AND cs.project_id = lc.project_id
        AND cs.kind = 'connection' AND cs.entity_id = lc.id
    LEFT JOIN expression e ON e.workspace_id = lc.workspace_id AND e.project_id = lc.project_id
        AND e.connection_id = lc.id AND e.field = 'condition'
    WHERE lc.workspace_id = n.workspace_id AND lc.project_id = n.project_id AND lc.from_node_id = n.id
) c ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS conditions
    FROM live_branch_output lo
    JOIN expression e ON e.workspace_id = lo.workspace_id AND e.project_id = lo.project_id
        AND e.branch_output_id = lo.id AND e.field = 'condition' AND e.source <> ''
    WHERE lo.workspace_id = n.workspace_id AND lo.project_id = n.project_id AND lo.node_id = n.id
) o ON true
WHERE n.workspace_id = @workspace_id AND n.project_id = @project_id AND n.id = @id;
