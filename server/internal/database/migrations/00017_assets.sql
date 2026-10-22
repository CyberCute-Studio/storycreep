-- +goose Up

-- Assets (PRD FR-ASSET-01, FR-ASSET-02, §5.3): the images that a project's
-- users upload, as node covers, component portraits, and board backgrounds,
-- whose files a store of package blob keeps, the local driver's directory or
-- a bucket of an S3-compatible object storage, under a key made of the
-- workspace and the hash of the content, {workspace}/{sha256}.{ext}, so that
-- the same file uploaded twice in a workspace is stored once. Package asset
-- uploads them, checks every read of one, and serves it.
--
-- The bucket that first-run setup chose for them (FR-ADMIN-02), which
-- STORYCREEP_ASSET_STORAGE overrides for as long as it is set, or none, which
-- leaves them to the local driver. Its columns are all set, or all null.
ALTER TABLE instance
	ADD COLUMN s3_host text,
	ADD COLUMN s3_port integer CHECK (s3_port BETWEEN 1 AND 65535),
	ADD COLUMN s3_security text CHECK (s3_security IN ('tls', 'none')),
	ADD COLUMN s3_bucket text,
	ADD COLUMN s3_region text,
	ADD COLUMN s3_access_key_id text,
	ADD COLUMN s3_secret_access_key text,
	ADD CONSTRAINT instance_s3_complete CHECK (
		num_nulls(s3_host, s3_port, s3_security, s3_bucket, s3_region, s3_access_key_id, s3_secret_access_key)
			IN (0, 7)
	);
GRANT UPDATE (s3_host, s3_port, s3_security, s3_bucket, s3_region, s3_access_key_id, s3_secret_access_key)
	ON instance TO storycreep_app;

-- The claim of the store that the orphan reconciliation of package asset
-- writes each hour to the store's blob storycreep-owner, with the
-- database's identity, so that it deletes blobs only in a store that its
-- database owns: the token that it wrote last, and the one before, which
-- the blob still holds if the store refused the last, and when it last
-- found there a token of the database's identity that this database did not
-- write, as a copy of its cluster's files, which has the same identity,
-- writes, which keeps both from deleting for a week, or null.
ALTER TABLE instance
	ADD COLUMN asset_store_claim text,
	ADD COLUMN asset_store_claim_previous text,
	ADD COLUMN asset_store_conflict_at timestamptz;
GRANT UPDATE (asset_store_claim, asset_store_claim_previous, asset_store_conflict_at) ON instance
	TO storycreep_app;

-- An asset of a project: one row for each file that the project's users
-- uploaded, which an upload of the same file to the project again answers
-- with, rather than adding another. Rows of the workspace that have the same
-- content share its blob, storage_key, which counts once towards the
-- workspace's storage_bytes (FR-ENT-01), and is deleted once no row refers to
-- it any longer (the orphan reconciliation of package asset). A row is never
-- changed, but for its thumbnail, which a job derives once the row exists.
-- PRD §5.3 lets a row belong to the workspace alone, without a project,
-- which no route makes yet.
CREATE TABLE asset (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid,
	-- The SHA-256 hash of the file as it is stored, which, for an SVG, is
	-- the file once its scripts and external references are removed.
	sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
	-- The type that the server found the file to be, from its content.
	mime text NOT NULL CHECK (mime IN ('image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml')),
	bytes bigint NOT NULL CHECK (bytes > 0),
	-- The image's size in pixels, or null for an SVG that does not say it, or
	-- says one of more than 16,384 pixels on a side.
	width integer CHECK (width > 0),
	height integer CHECK (height > 0),
	-- The key of the file in the store, {workspace}/{sha256}.{ext}, and of
	-- its thumbnail: the file's own for an image that needs none, which is
	-- its own thumbnail, as one that does not decode is, or else null until
	-- the thumbnail job has made one.
	storage_key text NOT NULL,
	thumbnail_key text,
	-- The user who uploaded it, or null once their account is deleted.
	uploaded_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	-- A project has one row for each content, and so does the workspace
	-- itself, for the rows without a project.
	UNIQUE NULLS NOT DISTINCT (workspace_id, project_id, sha256),
	CHECK ((width IS NULL) = (height IS NULL))
);
-- The orphan reconciliation and the upload find the rows that refer to a
-- blob by it, and the thumbnail job the rows of a content.
CREATE INDEX asset_storage_key ON asset (workspace_id, storage_key);
CREATE INDEX asset_sha256 ON asset (workspace_id, sha256);
CREATE INDEX asset_thumbnail_key ON asset (workspace_id, thumbnail_key) WHERE thumbnail_key IS NOT NULL;
CREATE INDEX asset_uploaded_by ON asset (uploaded_by);

ALTER TABLE asset ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON asset TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application adds the rows and records their thumbnails; the cascade of
-- a project's purge, or a workspace's deletion, deletes them.
GRANT SELECT, INSERT ON asset TO storycreep_app;
GRANT UPDATE (thumbnail_key) ON asset TO storycreep_app;
GRANT SELECT ON asset TO storycreep_maintenance;
