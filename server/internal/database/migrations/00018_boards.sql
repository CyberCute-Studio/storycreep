-- +goose Up

-- Boards and folders (PRD FR-PROJ-02, §5.3): the named canvases of a project,
-- organised in a tree of folders, which package board keeps. Every change of
-- a board or a folder is a command of the pipeline (package commands), which
-- increments its rev (§5.7, ADR-006).
--
-- A slug is groups of lowercase ASCII letters and digits joined by single
-- hyphens, of at most 64 characters, and never a name that Windows reserves
-- for a device, because a board's slug names its file in the game project
-- (FR-PROJ-02 AC4, §6.3): package slug checks the same.

-- A project's slug, which the published format's manifest shows (§6.5): the
-- server derives it from the project's name when it creates the project, as
-- it derives a board's, and only a change of the slug itself changes it, never
-- a rename. Projects do not share files, so two may have the same slug. The
-- projects that exist already are given one from their names as package slug
-- derives it (slug.Derive): the name in Unicode's compatibility decomposition
-- (NFKD), without the accents and the other marks that it puts after a
-- letter, with the letters that do not decompose, as the German sharp s,
-- spelled in ASCII as slug.Base spells them, and in lowercase, each run of
-- what is then not an ASCII letter or digit a hyphen, so that a name in Czech
-- keeps its words whole. The patterns name those letters by their code
-- points, so that the migration holds no letter outside ASCII, and a database
-- whose encoding is not UTF-8, which normalize needs, keeps the name's letters
-- outside ASCII instead, each of which then splits the name as a space does.
-- Their owners can change it. A row added without one, as by a test, has
-- project.
ALTER TABLE project ADD COLUMN slug text NOT NULL DEFAULT 'project';
UPDATE project p SET slug = CASE
		WHEN d.s = '' THEN 'project'
		WHEN d.s IN ('aux', 'con', 'nul', 'prn', 'com0', 'com1', 'com2', 'com3', 'com4', 'com5', 'com6', 'com7',
			'com8', 'com9', 'lpt0', 'lpt1', 'lpt2', 'lpt3', 'lpt4', 'lpt5', 'lpt6', 'lpt7', 'lpt8', 'lpt9')
			THEN d.s || '-project'
		ELSE d.s
	END
FROM (
	SELECT id, rtrim(left(trim(BOTH '-' FROM regexp_replace(lower(folded COLLATE "C"), '[^a-z0-9]+', '-', 'g')),
		64), '-') AS s
	FROM (
		SELECT id, CASE WHEN current_setting('server_encoding') <> 'UTF8' THEN name ELSE
			regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(
			regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(
				normalize(name, NFKD),
				E'[\\u0300-\\u036f\\u1ab0-\\u1aff\\u1dc0-\\u1dff\\u20d0-\\u20ff\\ufe20-\\ufe2f]', '', 'g'),
				E'[\\u00df\\u1e9e]', 'ss', 'g'),
				E'[\\u00e6\\u00c6]', 'ae', 'g'),
				E'[\\u0153\\u0152]', 'oe', 'g'),
				E'[\\u00fe\\u00de]', 'th', 'g'),
				E'[\\u00f8\\u00d8]', 'o', 'g'),
				E'[\\u0142\\u0141]', 'l', 'g'),
				E'[\\u0111\\u0110\\u00f0\\u00d0]', 'd', 'g'),
				E'\\u0131', 'i', 'g'),
				E'[\\u0127\\u0126]', 'h', 'g')
		END AS folded
		FROM project
	) f
) d
WHERE p.id = d.id;
ALTER TABLE project ADD CONSTRAINT project_slug_form CHECK (
	slug COLLATE "C" ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 64
	AND slug NOT IN ('aux', 'con', 'nul', 'prn', 'com0', 'com1', 'com2', 'com3', 'com4', 'com5', 'com6', 'com7',
		'com8', 'com9', 'lpt0', 'lpt1', 'lpt2', 'lpt3', 'lpt4', 'lpt5', 'lpt6', 'lpt7', 'lpt8', 'lpt9')
);

-- A folder of a project's boards, variables, or components, as its kind says,
-- in a tree of the folders of its kind: its parent is a folder of the same
-- project and kind, or none for a folder at the top. A folder's children are
-- ordered among themselves by their order, a key of package orderkey, then by
-- their IDs, and the boards of a folder share that order with its folders, so
-- that a board can be placed between two folders. A deleted folder stays,
-- with deleted_at, so that the boards in the trash that it held still name it,
-- and restoring one of them restores the folder too; a folder is deleted only
-- once it holds nothing that is not in the trash. The purge of a project
-- whose time in the trash has passed deletes its folders, by the cascade of
-- their foreign key to it.
CREATE TABLE folder (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	kind text NOT NULL CHECK (kind IN ('board', 'variable', 'component')),
	parent_id uuid,
	name text NOT NULL,
	"order" text COLLATE "C" NOT NULL CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	-- Incremented by every command that changes the folder.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who created the folder and changed it last, or null once
	-- their accounts are deleted.
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	-- What the foreign keys that keep a folder's parent, and a board's
	-- folder, in the same project and of the right kind refer to.
	UNIQUE (workspace_id, project_id, kind, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	-- A folder that is deleted for good leaves its children at the top,
	-- rather than taking them with it.
	FOREIGN KEY (workspace_id, project_id, kind, parent_id) REFERENCES folder (workspace_id, project_id, kind, id)
		ON DELETE SET NULL (parent_id),
	CHECK (parent_id <> id)
);
CREATE INDEX folder_parent ON folder (workspace_id, project_id, parent_id);
CREATE INDEX folder_project ON folder (workspace_id, project_id, id);
CREATE INDEX folder_created_by ON folder (created_by);
CREATE INDEX folder_updated_by ON folder (updated_by);

ALTER TABLE folder ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON folder TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- A board of a project: a named canvas, in a folder of boards or at the top.
-- Its slug is unique within the project, the boards in its trash included, so
-- that restoring one never changes its slug (FR-PROJ-02 AC1), and compared by
-- its bytes, which a slug's ASCII sorts as its letters, so that the slugs
-- that start with a name's, which a new board's derivation reads, are a range
-- of the index of that uniqueness (package board). Its colour is
-- none, or a name of the palette that the web app maps to an accessible
-- colour. Its entry node is a node of the board, which the nodes (plan item
-- 35) set and check, and the validation reports one that no longer exists
-- (FR-DEP-04). A deleted board is in the project's trash, with deleted_at.
CREATE TABLE board (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	folder_id uuid,
	-- The kind of the board's folder, which the foreign key to the folder
	-- holds to folders of boards.
	folder_kind text NOT NULL DEFAULT 'board' CHECK (folder_kind = 'board'),
	"order" text COLLATE "C" NOT NULL CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	name text NOT NULL,
	slug text COLLATE "C" NOT NULL CHECK (
		slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 64
		AND slug NOT IN ('aux', 'con', 'nul', 'prn', 'com0', 'com1', 'com2', 'com3', 'com4', 'com5', 'com6', 'com7',
			'com8', 'com9', 'lpt0', 'lpt1', 'lpt2', 'lpt3', 'lpt4', 'lpt5', 'lpt6', 'lpt7', 'lpt8', 'lpt9')
	),
	description text NOT NULL DEFAULT '',
	color text NOT NULL DEFAULT 'none'
		CHECK (color IN ('none', 'red', 'orange', 'yellow', 'green', 'teal', 'blue', 'purple', 'pink', 'gray')),
	entry_node_id uuid,
	-- Incremented by every command that changes the board.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, slug),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	-- A folder that is deleted for good leaves its boards at the top.
	FOREIGN KEY (workspace_id, project_id, folder_kind, folder_id)
		REFERENCES folder (workspace_id, project_id, kind, id) ON DELETE SET NULL (folder_id)
);
CREATE INDEX board_folder ON board (workspace_id, project_id, folder_id);
CREATE INDEX board_project ON board (workspace_id, project_id, id);
CREATE INDEX board_created_by ON board (created_by);
CREATE INDEX board_updated_by ON board (updated_by);

ALTER TABLE board ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON board TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application adds the rows and changes them; a board or a folder is
-- deleted to the trash by a change of its deleted_at, and for good only by
-- the cascade of its project's purge, or of its workspace's deletion.
GRANT SELECT, INSERT, UPDATE ON folder, board TO storycreep_app;
GRANT SELECT ON folder, board TO storycreep_maintenance;
