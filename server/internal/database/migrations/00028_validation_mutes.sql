-- +goose Up

-- The mutes of the validation's findings (PRD FR-DEP-03 AC4, §5.3, plan note
-- N55), which package validation keeps. A mute silences one code of the
-- validation on one entity, its owner, a node, a connection, a branch's
-- output, a variable, or a board, exactly one of which it names, with the
-- reason that its author gave: the finding of that code on that entity is
-- listed apart, as muted, with the mute, never left out. Only a warning or an
-- info may be muted, never an error, which blocks publishing (FR-DEP-04), so
-- the codes are those of the warnings and the infos of each kind of owner.
-- A mute is part of its owner, as an expression is, without a rev or a
-- deleted_at of its own: muting and unmuting are commands of the pipeline
-- (validation.mute, validation.unmute), each a change of its owner, whose
-- rev it increments, and a mute is in the trash with its owner, and deleted
-- with it by the cascade of its foreign key, by the owner's purge.
-- The key by which a mute is of a variable of its project, as a board,
-- a node, a connection, and an output have theirs.
ALTER TABLE variable ADD CONSTRAINT variable_workspace_id_project_id_id_key UNIQUE (workspace_id, project_id, id);

CREATE TABLE validation_mute (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	node_id uuid,
	connection_id uuid,
	branch_output_id uuid,
	variable_id uuid,
	board_id uuid,
	code text NOT NULL,
	-- At most 500 characters, as the commands' payloads take (package
	-- validation, MaxReasonLength): change both at once.
	reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
	-- The users who muted the finding first and last, the latter with the
	-- reason that the mute holds, or null once their accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, node_id) REFERENCES node (workspace_id, project_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, connection_id) REFERENCES connection (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, branch_output_id) REFERENCES branch_output (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, variable_id) REFERENCES variable (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, board_id) REFERENCES board (workspace_id, project_id, id)
		ON DELETE CASCADE,
	CHECK (num_nonnulls(node_id, connection_id, branch_output_id, variable_id, board_id) = 1),
	-- The warnings and the infos of each kind of owner (package validation,
	-- Rules): change both at once.
	CHECK (CASE
		WHEN node_id IS NOT NULL THEN code IN ('node_unreachable', 'node_empty', 'dead_end', 'speaker_missing')
		WHEN connection_id IS NOT NULL THEN code IN ('choice_duplicate_label', 'connection_self',
			'connection_parallel')
		WHEN branch_output_id IS NOT NULL THEN code = 'port_unconnected'
		WHEN variable_id IS NOT NULL THEN code IN ('var_read_never_written', 'var_written_never_read')
		ELSE code = 'board_entry_missing'
	END)
);
-- An owner has one mute of a code at most, and a project's mutes are read by
-- this index. The owners' UUIDs are of different entities, so they never
-- collide.
CREATE UNIQUE INDEX validation_mute_owner ON validation_mute (workspace_id, project_id,
	coalesce(node_id, connection_id, branch_output_id, variable_id, board_id), code);
-- The cascades of the owners' purges, and of the accounts' deletions.
CREATE INDEX validation_mute_node ON validation_mute (workspace_id, project_id, node_id) WHERE node_id IS NOT NULL;
CREATE INDEX validation_mute_connection ON validation_mute (workspace_id, project_id, connection_id)
	WHERE connection_id IS NOT NULL;
CREATE INDEX validation_mute_branch_output ON validation_mute (workspace_id, project_id, branch_output_id)
	WHERE branch_output_id IS NOT NULL;
CREATE INDEX validation_mute_variable ON validation_mute (workspace_id, project_id, variable_id)
	WHERE variable_id IS NOT NULL;
CREATE INDEX validation_mute_board ON validation_mute (workspace_id, project_id, board_id)
	WHERE board_id IS NOT NULL;
CREATE INDEX validation_mute_created_by ON validation_mute (created_by);
CREATE INDEX validation_mute_updated_by ON validation_mute (updated_by);

ALTER TABLE validation_mute ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON validation_mute TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application adds a mute, changes its reason, and deletes it when it is
-- unmuted, and the cascade of its owner's purge deletes it.
GRANT SELECT, INSERT, UPDATE, DELETE ON validation_mute TO storycreep_app;
GRANT SELECT ON validation_mute TO storycreep_maintenance;
