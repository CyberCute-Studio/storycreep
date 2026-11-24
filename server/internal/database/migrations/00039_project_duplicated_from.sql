-- +goose Up

-- Project duplication (PRD FR-PROJ-01, plan item 85, note N92): a copy of a
-- project records the project that it was duplicated from, of its own
-- workspace, until that project is purged, which leaves the copy as it is and
-- without the record, as the audit log keeps who duplicated what, and when.
ALTER TABLE project
	ADD COLUMN duplicated_from uuid,
	ADD CONSTRAINT project_duplicated_from_fkey FOREIGN KEY (workspace_id, duplicated_from)
		REFERENCES project (workspace_id, id) ON DELETE SET NULL (duplicated_from);
-- What the purge of a project finds its copies by.
CREATE INDEX project_duplicated_from ON project (workspace_id, duplicated_from);
