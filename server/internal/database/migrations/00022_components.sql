-- +goose Up

-- Component types, their attribute definitions, components, and the values
-- of their attributes (PRD FR-COMP-01, FR-COMP-02, §5.3, plan note N42): the
-- characters, items, locations, and factions of a project's narrative, each
-- of a type whose ordered attributes its components hold values of, which
-- package component keeps. Every change of them is a command of the pipeline
-- (package commands), which increments the changed rows' rev (§5.7, ADR-006);
-- a component's values are part of the component, whose rev a change of one
-- increments, and have no rev of their own.

-- What a component's image refers to, which keeps it to an asset of the
-- component's project.
ALTER TABLE asset ADD CONSTRAINT asset_workspace_id_project_id_id_key UNIQUE (workspace_id, project_id, id);

-- A UUIDv7 (PRD §5.2) of the clock, shift_ms milliseconds on, and random
-- bits, for a row that the database adds itself, as a component's value of a
-- new attribute definition, or a default type of a project that exists.
CREATE FUNCTION uuid_v7(shift_ms bigint DEFAULT 0) RETURNS uuid
	LANGUAGE sql VOLATILE PARALLEL SAFE
	RETURN encode(set_bit(set_bit(overlay(uuid_send(gen_random_uuid()) PLACING substring(int8send(
		floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint + shift_ms) FROM 3) FROM 1 FOR 6), 52, 1), 53, 1),
		'hex')::uuid;

-- Whether options are the options of an enum: 1 to 100 of them, each of 1 to
-- 100 characters, and no two alike (package value, CheckOptions).
CREATE FUNCTION enum_options_valid(options text[]) RETURNS boolean
	LANGUAGE sql IMMUTABLE PARALLEL SAFE
	RETURN cardinality(options) BETWEEN 1 AND 100 AND array_position(options, NULL) IS NULL
		AND NOT EXISTS (SELECT FROM unnest(options) o WHERE length(o) NOT BETWEEN 1 AND 100)
		AND cardinality(options) = (SELECT count(DISTINCT o COLLATE "C") FROM unnest(options) o);

-- A type of component of a project, as Character or Item, whose attribute
-- definitions its components hold values of (FR-COMP-01). Its key is an
-- identifier of the expression language (§5.8.1, package ident), compared by
-- its bytes, unique among the project's types that are not in the trash. Its
-- icon is one of a few names, which the web app maps to a glyph. A deleted
-- type is in the project's trash, unlisted, with deleted_at, as a deleted
-- folder is, and is deleted only once each of its components is in the trash
-- too; restoring one of them restores it. Its attribute definitions are in
-- the trash with it.
CREATE TABLE component_type (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	key text COLLATE "C" NOT NULL CHECK (
		key ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND length(key) <= 64
		AND key NOT IN ('true', 'false', 'and', 'or', 'not', 'global', 'board', 'null', 'if', 'else', 'elseif')
	),
	-- At most 100 characters, as the commands' payloads take (package
	-- component, MaxNameLength), as a board's name is (00021).
	name text NOT NULL CHECK (length(name) <= 100),
	icon text NOT NULL DEFAULT 'none' CHECK (icon IN ('none', 'person', 'group', 'item', 'location', 'event',
		'book', 'star', 'heart', 'key', 'gem', 'shield', 'flag', 'tag')),
	-- Incremented by every command that changes the type.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	-- The users who created the type and changed it last, or null once their
	-- accounts are deleted, or for a default type that this migration gave a
	-- project that existed before it. A default type that the server adds as
	-- it creates a project names the user who creates it (package component,
	-- Seed).
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
-- The keys of the types that are not in the trash, unique within their
-- project, which a type's key is checked against.
CREATE UNIQUE INDEX component_type_key ON component_type (workspace_id, project_id, key) WHERE deleted_at IS NULL;
CREATE INDEX component_type_created_by ON component_type (created_by);
CREATE INDEX component_type_updated_by ON component_type (updated_by);
-- The types of each project's trash, by when they were deleted, which the
-- trash's purge reads, oldest first.
CREATE INDEX component_type_trash ON component_type (workspace_id, project_id, deleted_at)
	WHERE deleted_at IS NOT NULL;

-- An attribute definition of a component type (FR-COMP-01): its key, an
-- identifier unique among its type's definitions that are not in the trash,
-- its name, its type, its enum's options, its default, a value of its type,
-- and whether it is part of the game's state (runtime), which a text, game
-- state of no kind, never is. Its default is a JSON value of its type, as a
-- variable's is (00020), a text a string of at most 100,000 characters, and
-- an enum one of its options, which an enum has 1 to 100 of. It sits among
-- its type's definitions, ordered by its order, a key of package orderkey,
-- then by its ID. A deleted definition is in the project's trash, unlisted,
-- with deleted_at, and keeps the values that its components hold, which
-- restoring it brings back; a definition of a type in the trash is in the
-- trash with it. The purge of a type deletes its definitions, by the cascade
-- of their foreign key to it, and the purge of a definition its values.
CREATE TABLE attribute_def (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	component_type_id uuid NOT NULL,
	"order" text COLLATE "C" NOT NULL CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	key text COLLATE "C" NOT NULL CHECK (
		key ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND length(key) <= 64
		AND key NOT IN ('true', 'false', 'and', 'or', 'not', 'global', 'board', 'null', 'if', 'else', 'elseif')
	),
	name text NOT NULL CHECK (length(name) <= 100),
	type text NOT NULL CHECK (type IN ('bool', 'int', 'float', 'string', 'text', 'enum')),
	enum_options text[] NOT NULL DEFAULT '{}',
	default_value jsonb NOT NULL,
	is_runtime boolean NOT NULL DEFAULT false,
	-- Incremented by every command that changes the definition.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, component_type_id) REFERENCES component_type (workspace_id, project_id, id)
		ON DELETE CASCADE,
	CHECK (CASE WHEN type = 'enum' THEN enum_options_valid(enum_options) ELSE cardinality(enum_options) = 0 END),
	CHECK (type <> 'text' OR NOT is_runtime),
	-- Each branch reads the default as a number only once it is one.
	CHECK (CASE type
		WHEN 'bool' THEN jsonb_typeof(default_value) = 'boolean'
		WHEN 'string' THEN jsonb_typeof(default_value) = 'string' AND length(default_value #>> '{}') <= 4096
		WHEN 'text' THEN jsonb_typeof(default_value) = 'string' AND length(default_value #>> '{}') <= 100000
		WHEN 'enum' THEN jsonb_typeof(default_value) = 'string' AND (default_value #>> '{}') = ANY (enum_options)
		WHEN 'int' THEN CASE WHEN jsonb_typeof(default_value) = 'number' THEN
			(default_value #>> '{}')::numeric = trunc((default_value #>> '{}')::numeric)
			AND abs((default_value #>> '{}')::numeric) <= 9007199254740991 ELSE false END
		WHEN 'float' THEN CASE WHEN jsonb_typeof(default_value) = 'number' THEN
			abs((default_value #>> '{}')::numeric) <= 1.7976931348623157e308 ELSE false END
		ELSE false
	END)
);
-- The keys of each type's definitions that are not in the trash, unique
-- within it.
CREATE UNIQUE INDEX attribute_def_key ON attribute_def (workspace_id, project_id, component_type_id, key)
	WHERE deleted_at IS NULL;
CREATE INDEX attribute_def_type ON attribute_def (workspace_id, project_id, component_type_id);
CREATE INDEX attribute_def_created_by ON attribute_def (created_by);
CREATE INDEX attribute_def_updated_by ON attribute_def (updated_by);
CREATE INDEX attribute_def_trash ON attribute_def (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

-- A component of a project, as a character (FR-COMP-02): its type, its key,
-- an identifier unique among the project's components that are not in the
-- trash, its name, its description, a rich-text document in its canonical
-- form (package richtext), and its image, an asset of the project. It sits
-- in a folder of components, or at the top, ordered among the folder's
-- children by its order, a key of package orderkey, as a variable is among a
-- folder of variables'. A deleted component is in the project's trash, with
-- deleted_at. Its type is fixed once it is created.
CREATE TABLE component (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	component_type_id uuid NOT NULL,
	folder_id uuid,
	-- The kind of the component's folder, which the foreign key to the folder
	-- holds to folders of components.
	folder_kind text NOT NULL DEFAULT 'component' CHECK (folder_kind = 'component'),
	"order" text COLLATE "C" NOT NULL CHECK ("order" ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND length("order") <= 128),
	key text COLLATE "C" NOT NULL CHECK (
		key ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND length(key) <= 64
		AND key NOT IN ('true', 'false', 'and', 'or', 'not', 'global', 'board', 'null', 'if', 'else', 'elseif')
	),
	name text NOT NULL CHECK (length(name) <= 100),
	description jsonb NOT NULL DEFAULT '{"type": "doc", "content": [{"type": "paragraph"}]}'
		CHECK (jsonb_typeof(description) = 'object'),
	image_asset_id uuid,
	-- Incremented by every command that changes the component or one of its
	-- values.
	rev bigint NOT NULL DEFAULT 1 CHECK (rev >= 1),
	deleted_at timestamptz,
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	updated_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	UNIQUE (workspace_id, project_id, id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	-- The purge deletes a type only once it holds no component, in the trash
	-- or not, so the cascade reaches a component only as its project's
	-- deletion does.
	FOREIGN KEY (workspace_id, project_id, component_type_id) REFERENCES component_type (workspace_id, project_id, id)
		ON DELETE CASCADE,
	-- A folder that is deleted for good leaves its components at the top.
	FOREIGN KEY (workspace_id, project_id, folder_kind, folder_id)
		REFERENCES folder (workspace_id, project_id, kind, id) ON DELETE SET NULL (folder_id),
	FOREIGN KEY (workspace_id, project_id, image_asset_id) REFERENCES asset (workspace_id, project_id, id)
		ON DELETE SET NULL (image_asset_id)
);
-- The keys of the components that are not in the trash, unique within their
-- project.
CREATE UNIQUE INDEX component_key ON component (workspace_id, project_id, key) WHERE deleted_at IS NULL;
CREATE INDEX component_type_components ON component (workspace_id, project_id, component_type_id, id);
CREATE INDEX component_folder ON component (workspace_id, project_id, folder_id);
CREATE INDEX component_image ON component (workspace_id, project_id, image_asset_id) WHERE image_asset_id IS NOT NULL;
CREATE INDEX component_created_by ON component (created_by);
CREATE INDEX component_updated_by ON component (updated_by);
-- The components of each project's trash, by when they were deleted, which
-- the trash's list reads, newest first, and its purge, oldest first.
CREATE INDEX component_trash ON component (workspace_id, project_id, deleted_at) WHERE deleted_at IS NOT NULL;

-- The value of an attribute that a component holds: one for each definition
-- of its type, a JSON value of the definition's type, which a command checks.
-- Adding a definition gives each component of its type its default, and a
-- change of the definition's default leaves the values as they are. A value
-- has no rev of its own: a change of it is a change of its component.
CREATE TABLE attribute_value (
	id uuid PRIMARY KEY DEFAULT uuid_v7(),
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	component_id uuid NOT NULL,
	attribute_def_id uuid NOT NULL,
	value jsonb NOT NULL CHECK (CASE jsonb_typeof(value)
		WHEN 'boolean' THEN true
		WHEN 'string' THEN length(value #>> '{}') <= 100000
		WHEN 'number' THEN abs((value #>> '{}')::numeric) <= 1.7976931348623157e308
		ELSE false
	END),
	UNIQUE (workspace_id, project_id, component_id, attribute_def_id),
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, component_id) REFERENCES component (workspace_id, project_id, id)
		ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id, attribute_def_id) REFERENCES attribute_def (workspace_id, project_id, id)
		ON DELETE CASCADE
);
CREATE INDEX attribute_value_attribute ON attribute_value (workspace_id, project_id, attribute_def_id, component_id);

ALTER TABLE component_type ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON component_type TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
ALTER TABLE attribute_def ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON attribute_def TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
ALTER TABLE component ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON component TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
ALTER TABLE attribute_value ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON attribute_value TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- What is not in the project's trash (package trash), which everything that
-- reads the project's content reads it through: the types and the
-- components that are not deleted, and the attribute definitions that are
-- not deleted themselves, nor of a type that is.
CREATE VIEW live_component_type WITH (security_invoker = true) AS
	SELECT t.* FROM component_type t WHERE t.deleted_at IS NULL;
CREATE VIEW live_attribute_def WITH (security_invoker = true) AS
	SELECT a.* FROM attribute_def a
	WHERE a.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM component_type t WHERE t.workspace_id = a.workspace_id
		AND t.project_id = a.project_id AND t.id = a.component_type_id AND t.deleted_at IS NOT NULL);
CREATE VIEW live_component WITH (security_invoker = true) AS
	SELECT c.* FROM component c WHERE c.deleted_at IS NULL;

-- The application adds the rows and changes them, deletes them to the trash
-- by a change of their deleted_at, and for good in the trash's purge, and
-- deletes and adds values.
GRANT SELECT, INSERT, UPDATE, DELETE ON component_type, attribute_def, component, attribute_value TO storycreep_app;
GRANT SELECT ON component_type, attribute_def, component, attribute_value TO storycreep_maintenance;
GRANT SELECT ON live_component_type, live_attribute_def, live_component TO storycreep_app, storycreep_maintenance;

-- The projects that exist get the default types that a project is created
-- with (package project), named in English, with IDs of UUIDv7 made from the
-- clock, a millisecond apart, so that they are listed in this order.
INSERT INTO component_type (id, workspace_id, project_id, key, name, icon, created_by, updated_by)
SELECT uuid_v7(d.i), p.workspace_id, p.id, d.key, d.name, d.icon, NULL, NULL
FROM project p
CROSS JOIN (VALUES (0, 'character', 'Character', 'person'), (1, 'item', 'Item', 'item'),
	(2, 'location', 'Location', 'location'), (3, 'faction', 'Faction', 'group'), (4, 'custom', 'Custom', 'tag'))
	AS d (i, key, name, icon);
