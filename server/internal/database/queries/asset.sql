-- Assets (package asset, PRD FR-ASSET-01, FR-ASSET-02): the files that a
-- project's users upload, whose blobs a store of package blob keeps. Every
-- query runs in a transaction of the asset's workspace, under row-level
-- security, and names the workspace too.

-- name: LockBlob :exec
-- LockBlob takes the transaction-scoped advisory lock of a blob of a
-- workspace, by its content's hash (database.BlobLock), until the
-- transaction ends.
SELECT pg_advisory_xact_lock(@class::integer, @object::integer);

-- name: KeepSession :exec
-- KeepSession sets, until the transaction ends, the timeouts that the
-- server, the database, or the login may set, and that end a session whose
-- transaction runs for long, or waits idle, as for the store, and with it
-- the advisory locks that the transaction holds (LockBlob): it switches off
-- the one of a transaction that runs for long, as database.timeoutsOff does
-- for the migrations' lock, and makes the one of a transaction that waits
-- idle idle_timeout milliseconds, which the caller makes longer than it ever
-- waits, so that the session of a client that went away without closing its
-- connection, as when its host failed, still ends within that time, and its
-- locks with it, rather than once TCP's keepalive finds the connection dead,
-- hours later. It sets each of them that the server has, as PostgreSQL 17
-- added transaction_timeout.
SELECT set_config(name, CASE name WHEN 'idle_in_transaction_session_timeout' THEN @idle_timeout::text ELSE '0' END,
    true)
FROM pg_catalog.pg_settings
WHERE name IN ('idle_in_transaction_session_timeout', 'transaction_timeout');

-- name: ProjectAsset :one
-- ProjectAsset returns the project's asset of the content sha256, if it has
-- one.
SELECT * FROM asset WHERE workspace_id = @workspace_id AND project_id = @project_id AND sha256 = @sha256;

-- name: ReferencedBlobs :many
-- ReferencedBlobs returns those of the keys that a row of the workspace
-- refers to, as the file of an asset or as its thumbnail: the check that
-- keeps a blob from the orphan reconciliation, which an upload makes of its
-- one blob, and the reconciliation of a batch of blobs in one query.
SELECT k.key::text AS key FROM unnest(@keys::text[]) AS k (key)
WHERE EXISTS (
    SELECT FROM asset a WHERE a.workspace_id = @workspace_id AND a.storage_key = k.key
) OR EXISTS (
    SELECT FROM asset a WHERE a.workspace_id = @workspace_id AND a.thumbnail_key = k.key
);

-- name: InsertAsset :one
-- InsertAsset adds an asset, and returns when it was created.
INSERT INTO asset (id, workspace_id, project_id, sha256, mime, bytes, width, height, storage_key, thumbnail_key,
    uploaded_by)
VALUES (@id, @workspace_id, @project_id, @sha256, @mime, @bytes, @width, @height, @storage_key, @thumbnail_key,
    @uploaded_by)
RETURNING created_at;

-- name: Asset :one
-- Asset returns the asset of the project.
SELECT * FROM asset WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: AssetByID :one
-- AssetByID returns the asset of the workspace, whatever its project, for
-- the thumbnail job.
SELECT * FROM asset WHERE workspace_id = @workspace_id AND id = @id;

-- name: ContentThumbnail :one
-- ContentThumbnail returns the thumbnail of an asset of the workspace whose
-- file is the content sha256, which an asset of the same content shares, if
-- one has one.
SELECT a.thumbnail_key::text AS thumbnail_key FROM asset a
WHERE a.workspace_id = @workspace_id AND a.sha256 = @sha256 AND a.thumbnail_key IS NOT NULL
LIMIT 1;

-- name: SetThumbnail :exec
-- SetThumbnail records the thumbnail of every asset of the workspace whose
-- file is the content sha256, and that has none yet, which share it.
UPDATE asset SET thumbnail_key = @thumbnail_key
WHERE workspace_id = @workspace_id AND sha256 = @sha256 AND thumbnail_key IS NULL;

-- name: StorageDrift :one
-- StorageDrift returns how many bytes the workspace's blobs take up, each
-- asset's file once however many assets share it, less its usage of
-- storage_bytes: what reconciliation adds to its usage so that the usage is
-- the count again. The thumbnails that the server derives do not count.
SELECT (
    coalesce((SELECT sum(b.bytes) FROM (
        SELECT DISTINCT ON (a.storage_key) a.bytes FROM asset a
        WHERE a.workspace_id = @workspace_id
        ORDER BY a.storage_key
    ) b), 0)
    - coalesce((SELECT c.value FROM usage_counter c WHERE c.workspace_id = @workspace_id
        AND c.key = 'storage_bytes'), 0)
    - coalesce((SELECT sum(d.delta) FROM usage_delta d WHERE d.workspace_id = @workspace_id
        AND d.key = 'storage_bytes'), 0)
)::bigint AS drift;

-- name: ShareProject :exec
-- ShareProject locks the project's row FOR SHARE until the transaction ends,
-- so that it is neither archived nor moved to the trash until then, while an
-- upload adds its asset, which reads the project once it holds the lock.
SELECT FROM project
WHERE workspace_id = @workspace_id AND id = @id
FOR SHARE;

-- name: ContentKeys :many
-- ContentKeys returns each content of the workspace's assets whose blob, or
-- thumbnail, is one of the keys, with the key of its blob, the key of its
-- thumbnail, or null until it has one, and the ID of one of its assets: the
-- keys of those blobs that the assets refer to, as their files or their
-- thumbnails, each once, and what the orphan reconciliation mends of the
-- blobs that it listed. A content's assets share its thumbnail, so each
-- content is returned once, unless a change of the thumbnail that has not
-- yet reached every asset is seen, which no transaction commits.
SELECT DISTINCT ON (a.sha256, a.thumbnail_key) a.id, a.sha256, a.storage_key, a.thumbnail_key FROM asset a
WHERE a.workspace_id = @workspace_id
    AND (a.storage_key = ANY(@keys::text[]) OR a.thumbnail_key = ANY(@keys::text[]))
ORDER BY a.sha256, a.thumbnail_key, a.id;

-- name: ForgetThumbnail :execrows
-- ForgetThumbnail records that the assets of the workspace whose file is
-- the content sha256, and whose thumbnail is the key, have none, as when the
-- store lost the thumbnail, so that its job makes it again, and returns how
-- many it changed.
UPDATE asset SET thumbnail_key = NULL
WHERE workspace_id = @workspace_id AND sha256 = @sha256 AND thumbnail_key = @thumbnail_key;
