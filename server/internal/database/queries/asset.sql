-- Assets (package asset, PRD FR-ASSET-01, FR-ASSET-02): the files that a
-- project's users upload, whose blobs a store of package blob keeps. Every
-- query runs in a transaction of the asset's workspace, under row-level
-- security, and names the workspace too.

-- name: LockBlob :exec
-- LockBlob takes the transaction-scoped advisory lock of a blob of a
-- workspace, by its content's hash (database.BlobLock), until the
-- transaction ends.
SELECT pg_advisory_xact_lock(@class::integer, @object::integer);

-- name: ProjectAsset :one
-- ProjectAsset returns the project's asset of the content sha256, if it has
-- one.
SELECT * FROM asset WHERE workspace_id = @workspace_id AND project_id = @project_id AND sha256 = @sha256;

-- name: BlobReferenced :one
-- BlobReferenced reports whether a row of the workspace refers to the key,
-- as the file of an asset or as its thumbnail.
SELECT (EXISTS (
    SELECT FROM asset a WHERE a.workspace_id = @workspace_id AND a.storage_key = @key
) OR EXISTS (
    SELECT FROM asset a WHERE a.workspace_id = @workspace_id AND a.thumbnail_key = @key
))::boolean AS referenced;

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

-- name: ReferencedKeys :many
-- ReferencedKeys returns the keys of the blobs that the workspace's assets
-- refer to, as their files or their thumbnails, each once.
SELECT a.storage_key AS key FROM asset a WHERE a.workspace_id = @workspace_id
UNION
SELECT a.thumbnail_key FROM asset a WHERE a.workspace_id = @workspace_id AND a.thumbnail_key IS NOT NULL;
