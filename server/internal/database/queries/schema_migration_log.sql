-- The migration log of the projects (package schemaversion, PRD FR-DATA-03
-- AC2). schema_migration_log is a tenant table, which a transaction of its
-- workspace reads.

-- name: InsertSchemaMigrationStep :exec
-- InsertSchemaMigrationStep logs a step of the migration of a snapshot's
-- files, and does nothing where the snapshot's step from the version is
-- logged already: the migration's output, stored with it, holds no files of
-- that version to migrate again (PRD §6.4), so only a repeated log of the same
-- steps finds one.
INSERT INTO schema_migration_log (id, workspace_id, project_id, snapshot_id, from_version, to_version, entries)
VALUES (@id, @workspace_id, @project_id, @snapshot_id, @from_version, @to_version, @entries)
ON CONFLICT (workspace_id, project_id, snapshot_id, from_version) DO NOTHING;

-- name: SchemaMigrationSteps :many
-- SchemaMigrationSteps returns the project's logged steps, by their
-- snapshots' IDs, and each snapshot's the oldest first.
SELECT snapshot_id, from_version, to_version, entries, created_at
FROM schema_migration_log
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY snapshot_id, from_version;
