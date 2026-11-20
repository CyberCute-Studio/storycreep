-- The migration log of the projects (package schemaversion, PRD FR-DATA-03
-- AC2). schema_migration_log is a tenant table, which a transaction of its
-- workspace reads.

-- name: InsertSchemaMigrationStep :exec
-- InsertSchemaMigrationStep logs a step of the migration of a snapshot's
-- files, and where the snapshot's step from the version is logged already,
-- replaces its report, and when it was logged, with this run's, as after a fix
-- of the migration, so that the log tells of the files that are stored.
INSERT INTO schema_migration_log (id, workspace_id, project_id, snapshot_id, from_version, to_version, entries)
VALUES (@id, @workspace_id, @project_id, @snapshot_id, @from_version, @to_version, @entries)
ON CONFLICT (workspace_id, project_id, snapshot_id, from_version) DO UPDATE
SET to_version = EXCLUDED.to_version, entries = EXCLUDED.entries, created_at = now();

-- name: SchemaMigrationSteps :many
-- SchemaMigrationSteps returns the project's logged steps, by their
-- snapshots' IDs, and each snapshot's the oldest first.
SELECT snapshot_id, from_version, to_version, entries, created_at
FROM schema_migration_log
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY snapshot_id, from_version;
