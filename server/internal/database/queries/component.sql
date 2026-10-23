-- Component types, attribute definitions, components, and their values
-- (package component, PRD FR-COMP-01, FR-COMP-02). They are tenant tables,
-- which a transaction of their workspace reads. The commands of a project
-- apply one batch at a time, under the lock of the project's row, so what
-- these queries read of the project's components stays as it is until the
-- batch changes it. What is not in the project's trash is read through the
-- live views, live_component_type, live_attribute_def, and live_component
-- (package trash).

-- name: InsertComponentType :exec
-- InsertComponentType adds a component type.
INSERT INTO component_type (id, workspace_id, project_id, key, name, icon, created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @key, @name, @icon, sqlc.narg(created_by), sqlc.narg(created_by));

-- name: ComponentTypeAny :one
-- ComponentTypeAny returns a component type of the project, whether or not it
-- is in the trash, and no row if the project has no such type.
SELECT sqlc.embed(component_type) FROM component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: ComponentTypesByID :many
-- ComponentTypesByID returns the component types of the project whose IDs
-- are among ids, whether or not they are in the trash.
SELECT sqlc.embed(component_type) FROM component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: ComponentTypeRevs :many
-- ComponentTypeRevs returns the revisions of the project's component types
-- whose IDs are among ids, those in the trash included, for the pipeline's
-- check of the revisions that a batch expects.
SELECT id, rev FROM component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: ComponentTypes :many
-- ComponentTypes returns the project's component types that are not in the
-- trash, in the order of their IDs, after the type after, or from the first
-- if after is null.
SELECT sqlc.embed(live_component_type) FROM live_component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: ComponentType :one
-- ComponentType returns a component type of the project that is not in the
-- trash, and no row if the project has no such type, or it is.
SELECT sqlc.embed(live_component_type) FROM live_component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: ComponentTypeWithKey :one
-- ComponentTypeWithKey returns the ID of the project's component type that
-- is not in the trash whose key is key, and no row if none has it.
SELECT id FROM component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL AND key = @key;

-- name: ComponentTypeKeysFrom :many
-- ComponentTypeKeysFrom returns the keys of the project's component types
-- that are not in the trash that start with stem: the keys from stem up to
-- stem with a tilde after it, which every key that starts with stem sorts
-- before, because a key's characters sort before the tilde in the bytes that
-- keys are compared by, so that the index of their uniqueness finds them.
SELECT key FROM component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL
    AND key >= @stem::text AND key < @stem::text || '~';

-- name: ComponentTypeInUse :one
-- ComponentTypeInUse returns whether a component of the project that is not
-- in the trash is of the type component_type_id.
SELECT EXISTS (SELECT 1 FROM component
    WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = @component_type_id
        AND deleted_at IS NULL)::boolean AS in_use;

-- name: UpdateComponentTypeName :one
-- UpdateComponentTypeName renames the type, unless it has the name already,
-- and returns its new revision, or no row if it does.
UPDATE component_type SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND name <> @name
RETURNING rev;

-- name: UpdateComponentTypeKey :one
-- UpdateComponentTypeKey changes the type's key, unless it has the key
-- already, and returns its new revision, or no row if it does.
UPDATE component_type SET key = @key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND key <> @key
RETURNING rev;

-- name: UpdateComponentTypeIcon :one
-- UpdateComponentTypeIcon changes the type's icon, unless it has the icon
-- already, and returns its new revision, or no row if it does.
UPDATE component_type SET icon = @icon, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND icon <> @icon
RETURNING rev;

-- name: TrashComponentType :one
-- TrashComponentType deletes the type to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is.
UPDATE component_type SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreComponentType :one
-- RestoreComponentType takes the type out of the project's trash, unless it
-- is not there, and returns its new revision, or no row if it is not.
UPDATE component_type SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: InsertAttributeDef :exec
-- InsertAttributeDef adds an attribute definition.
INSERT INTO attribute_def (id, workspace_id, project_id, component_type_id, "order", key, name, type, enum_options,
    default_value, is_runtime, created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @component_type_id, @sort_key, @key, @name, @type, @enum_options,
    @default_value, @is_runtime, @created_by, @created_by);

-- name: AttributeDefAny :one
-- AttributeDefAny returns an attribute definition of the project, whether or
-- not it is in the trash, and whether its type is, which type_deleted says,
-- and no row if the project has no such definition.
SELECT sqlc.embed(attribute_def), (t.deleted_at IS NOT NULL)::boolean AS type_deleted
FROM attribute_def
JOIN component_type t ON t.workspace_id = attribute_def.workspace_id AND t.project_id = attribute_def.project_id
    AND t.id = attribute_def.component_type_id
WHERE attribute_def.workspace_id = @workspace_id AND attribute_def.project_id = @project_id
    AND attribute_def.id = @id;

-- name: AttributeDefsByID :many
-- AttributeDefsByID returns the attribute definitions of the project whose
-- IDs are among ids, whether or not they are in the trash, each with when its
-- type was deleted to the trash, as type_deleted_at, if it is there, which a
-- definition is in with it.
SELECT sqlc.embed(attribute_def), t.deleted_at AS type_deleted_at
FROM attribute_def
JOIN component_type t ON t.workspace_id = attribute_def.workspace_id AND t.project_id = attribute_def.project_id
    AND t.id = attribute_def.component_type_id
WHERE attribute_def.workspace_id = @workspace_id AND attribute_def.project_id = @project_id
    AND attribute_def.id = ANY(@ids::uuid[]);

-- name: AttributeDefRevs :many
-- AttributeDefRevs returns the revisions of the project's attribute
-- definitions whose IDs are among ids, those in the trash included.
SELECT id, rev FROM attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: TypeAttributeDefs :many
-- TypeAttributeDefs returns the attribute definitions that are not in the
-- trash of the project's types whose IDs are among type_ids, by their types,
-- each type's in their order: by their orders, then by their IDs.
SELECT sqlc.embed(live_attribute_def) FROM live_attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = ANY(@type_ids::uuid[])
ORDER BY component_type_id, "order", id;

-- name: AttributeDefWithKey :one
-- AttributeDefWithKey returns the ID of the attribute definition of the
-- type component_type_id that is not deleted whose key is key, and no row if
-- none has it.
SELECT id FROM attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = @component_type_id
    AND deleted_at IS NULL AND key = @key;

-- name: AttributeKeysFrom :many
-- AttributeKeysFrom returns the keys of the attribute definitions of the type
-- component_type_id that are not deleted that start with stem, as
-- ComponentTypeKeysFrom does.
SELECT key FROM attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = @component_type_id
    AND deleted_at IS NULL AND key >= @stem::text AND key < @stem::text || '~';

-- name: AttributeDefCount :one
-- AttributeDefCount returns how many attribute definitions of the type
-- component_type_id are not deleted.
SELECT count(*) FROM attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = @component_type_id
    AND deleted_at IS NULL;

-- name: LastAttributeSortKeys :one
-- LastAttributeSortKeys returns the greatest order of the attribute
-- definitions of the type component_type_id, but for the definition moved,
-- if it is not null, which moves among them, the deleted ones included, so
-- that one restored later keeps its place, as last, or an empty string if
-- there is none, and the last of those that are not deleted, in their order,
-- by their orders and then their IDs, as last_live, with its ID as
-- last_live_id, or an empty string and null if there is none.
WITH a (id, sort_key, deleted) AS (
    SELECT d.id, d."order", d.deleted_at IS NOT NULL FROM attribute_def d
    WHERE d.workspace_id = @workspace_id AND d.project_id = @project_id
        AND d.component_type_id = @component_type_id AND d.id IS DISTINCT FROM sqlc.narg(moved)::uuid
), live AS (
    SELECT a.id, a.sort_key FROM a WHERE NOT a.deleted ORDER BY a.sort_key DESC, a.id DESC LIMIT 1
)
SELECT coalesce((SELECT max(a.sort_key) FROM a), '')::text AS last,
    coalesce((SELECT live.sort_key FROM live), '')::text AS last_live,
    (SELECT live.id FROM live)::uuid AS last_live_id;

-- name: UpdateAttributeDefName :one
-- UpdateAttributeDefName renames the definition, unless it has the name
-- already, and returns its new revision, or no row if it does.
UPDATE attribute_def SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND name <> @name
RETURNING rev;

-- name: UpdateAttributeDefKey :one
-- UpdateAttributeDefKey changes the definition's key, unless it has the key
-- already, and returns its new revision, or no row if it does.
UPDATE attribute_def SET key = @key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND key <> @key
RETURNING rev;

-- name: UpdateAttributeDefRuntime :one
-- UpdateAttributeDefRuntime sets or unsets the definition's is_runtime,
-- unless it is so already, and returns its new revision, or no row if it is.
UPDATE attribute_def SET is_runtime = @is_runtime, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND is_runtime <> @is_runtime
RETURNING rev;

-- name: UpdateAttributeDefType :one
-- UpdateAttributeDefType changes the definition's type, its enum's options,
-- and its default, unless it has all of them already, and returns its new
-- revision, or no row if it does.
UPDATE attribute_def SET type = @type, enum_options = @enum_options, default_value = @default_value,
    rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (type <> @type OR enum_options <> @enum_options OR default_value <> @default_value)
RETURNING rev;

-- name: MoveAttributeDef :one
-- MoveAttributeDef places the definition at the order sort_key among its
-- type's, unless it is there already, and returns its new revision, or no row
-- if it is.
UPDATE attribute_def SET "order" = @sort_key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND "order" <> @sort_key
RETURNING rev;

-- name: TrashAttributeDef :one
-- TrashAttributeDef deletes the definition to the project's trash, unless it
-- is there already, and returns its new revision, or no row if it is.
UPDATE attribute_def SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreAttributeDef :one
-- RestoreAttributeDef takes the definition out of the project's trash,
-- unless it is not there, and returns its new revision, or no row if it is
-- not.
UPDATE attribute_def SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: InsertComponent :exec
-- InsertComponent adds a component.
INSERT INTO component (id, workspace_id, project_id, component_type_id, folder_id, "order", key, name, description,
    image_asset_id, created_by, updated_by)
VALUES (@id, @workspace_id, @project_id, @component_type_id, sqlc.narg(folder_id), @sort_key, @key, @name,
    @description, sqlc.narg(image_asset_id), @created_by, @created_by);

-- name: ComponentAny :one
-- ComponentAny returns a component of the project, whether or not it is in
-- the trash, and no row if the project has no such component.
SELECT sqlc.embed(component) FROM component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: ComponentsByID :many
-- ComponentsByID returns the components of the project whose IDs are among
-- ids, whether or not they are in the trash.
SELECT sqlc.embed(component) FROM component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: ComponentRevs :many
-- ComponentRevs returns the revisions of the project's components whose IDs
-- are among ids, those in the trash included.
SELECT id, rev FROM component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[]);

-- name: Components :many
-- Components returns the project's components that are not in the trash, in
-- the order of their IDs, after the component after, or from the first if
-- after is null: those of the type component_type_id if it is not null, and
-- those whose names or keys hold pattern, a pattern of ILIKE, if it is not
-- null.
SELECT sqlc.embed(live_component) FROM live_component
WHERE workspace_id = @workspace_id AND project_id = @project_id
    AND (sqlc.narg(component_type_id)::uuid IS NULL OR component_type_id = sqlc.narg(component_type_id)::uuid)
    AND (sqlc.narg(pattern)::text IS NULL OR name ILIKE sqlc.narg(pattern)::text
        OR key ILIKE sqlc.narg(pattern)::text)
    AND (sqlc.narg(after)::uuid IS NULL OR id > sqlc.narg(after)::uuid)
ORDER BY id
LIMIT @max_rows;

-- name: Component :one
-- Component returns a component of the project that is not in the trash, and
-- no row if the project has no such component, or it is.
SELECT sqlc.embed(live_component) FROM live_component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: ComponentWithKey :one
-- ComponentWithKey returns the ID of the project's component that is not in
-- the trash whose key is key, and no row if none has it.
SELECT id FROM component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL AND key = @key;

-- name: ComponentKeysFrom :many
-- ComponentKeysFrom returns the keys of the project's components that are not
-- in the trash that start with stem, as ComponentTypeKeysFrom does.
SELECT key FROM component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND deleted_at IS NULL
    AND key >= @stem::text AND key < @stem::text || '~';

-- name: UpdateComponentName :one
-- UpdateComponentName renames the component, unless it has the name
-- already, and returns its new revision, or no row if it does.
UPDATE component SET name = @name, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND name <> @name
RETURNING rev;

-- name: UpdateComponentKey :one
-- UpdateComponentKey changes the component's key, unless it has the key
-- already, and returns its new revision, or no row if it does.
UPDATE component SET key = @key, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND key <> @key
RETURNING rev;

-- name: UpdateComponentDescription :one
-- UpdateComponentDescription changes the component's description, unless it
-- has the description already, and returns its new revision, or no row if it
-- does.
UPDATE component SET description = @description, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND description <> @description
RETURNING rev;

-- name: UpdateComponentImage :one
-- UpdateComponentImage sets the component's image to the asset
-- image_asset_id, or none if it is null, unless it is so already, and returns
-- its new revision, or no row if it is.
UPDATE component SET image_asset_id = sqlc.narg(image_asset_id), rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND image_asset_id IS DISTINCT FROM sqlc.narg(image_asset_id)
RETURNING rev;

-- name: MoveComponent :one
-- MoveComponent moves the component into the folder folder_id, or to the top
-- if it is null, at the order sort_key, unless it is there already, and
-- returns its new revision, or no row if it is.
UPDATE component SET folder_id = sqlc.narg(folder_id), "order" = @sort_key, rev = rev + 1, updated_at = now(),
    updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id
    AND (folder_id IS DISTINCT FROM sqlc.narg(folder_id) OR "order" <> @sort_key)
RETURNING rev;

-- name: TrashComponent :one
-- TrashComponent deletes the component to the project's trash, unless it is
-- there already, and returns its new revision, or no row if it is.
UPDATE component SET deleted_at = now(), rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NULL
RETURNING rev;

-- name: RestoreComponent :one
-- RestoreComponent takes the component out of the project's trash, unless it
-- is not there, and returns its new revision, or no row if it is not.
UPDATE component SET deleted_at = NULL, rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id AND deleted_at IS NOT NULL
RETURNING rev;

-- name: TouchComponents :many
-- TouchComponents increments the revisions of the project's components whose
-- IDs are among ids, those in the trash included, a change of whose values
-- is a change of them, and returns their IDs and their new revisions.
UPDATE component SET rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
RETURNING id, rev;

-- name: TouchTypeComponents :many
-- TouchTypeComponents increments the revisions of the components of the type
-- component_type_id, those in the trash included, whose values a change of
-- one of the type's attribute definitions changes, and returns their IDs and
-- their new revisions.
UPDATE component SET rev = rev + 1, updated_at = now(), updated_by = @updated_by
WHERE workspace_id = @workspace_id AND project_id = @project_id AND component_type_id = @component_type_id
RETURNING id, rev;

-- name: ComponentValues :many
-- ComponentValues returns the values that the project's components whose IDs
-- are among ids hold of the attribute definitions that are not in the trash,
-- each with its definition's type and options, by their components, each
-- component's in the order of its definitions' IDs.
SELECT v.component_id, v.attribute_def_id, v.value, a.type, a.enum_options FROM attribute_value v
JOIN live_attribute_def a ON a.workspace_id = v.workspace_id AND a.project_id = v.project_id
    AND a.id = v.attribute_def_id
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.component_id = ANY(@ids::uuid[])
ORDER BY v.component_id, v.attribute_def_id;

-- name: AttributeValues :many
-- AttributeValues returns the values that the project's components hold of
-- the attribute definition attribute_def_id, those of the components in the
-- trash included, in the order of the components' IDs, after the component
-- after, or from the first if after is null, each with its component's key,
-- name, and when it was deleted, if it is in the trash.
SELECT v.component_id, v.value, c.key, c.name, c.deleted_at FROM attribute_value v
JOIN component c ON c.workspace_id = v.workspace_id AND c.project_id = v.project_id AND c.id = v.component_id
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.attribute_def_id = @attribute_def_id
    AND (sqlc.narg(after)::uuid IS NULL OR v.component_id > sqlc.narg(after)::uuid)
ORDER BY v.component_id
LIMIT @max_rows;

-- name: FillAttributeValues :many
-- FillAttributeValues gives each component of the type component_type_id,
-- those in the trash included, that holds no value of the attribute
-- definition attribute_def_id the value value, and returns their IDs.
INSERT INTO attribute_value (workspace_id, project_id, component_id, attribute_def_id, value)
SELECT c.workspace_id, c.project_id, c.id, @attribute_def_id, @value FROM component c
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.component_type_id = @component_type_id
ON CONFLICT DO NOTHING
RETURNING component_id;

-- name: InsertComponentValues :exec
-- InsertComponentValues gives the component component_id the values of the
-- object vals, the value of each attribute definition by its UUID.
INSERT INTO attribute_value (workspace_id, project_id, component_id, attribute_def_id, value)
SELECT @workspace_id, @project_id, @component_id, e.key::uuid, e.value FROM jsonb_each(@vals::jsonb) e;

-- name: SetAttributeValue :execrows
-- SetAttributeValue sets the value that the component component_id holds of
-- the attribute definition attribute_def_id to value, unless it holds it
-- already, and returns how many rows it changed: 1, or 0 if it does. A
-- component holds a value of each definition of its type, so the insert
-- only stands guard.
INSERT INTO attribute_value (workspace_id, project_id, component_id, attribute_def_id, value)
VALUES (@workspace_id, @project_id, @component_id, @attribute_def_id, @value)
ON CONFLICT (workspace_id, project_id, component_id, attribute_def_id) DO UPDATE SET value = EXCLUDED.value
WHERE attribute_value.value <> EXCLUDED.value;

-- name: SetAttributeValues :many
-- SetAttributeValues sets the values that the components hold of the
-- attribute definition attribute_def_id to those of the object vals, the
-- value of each component by its UUID, but for those that hold them already,
-- and returns the IDs of the components whose values it changed.
UPDATE attribute_value v SET value = e.value
FROM jsonb_each(@vals::jsonb) e
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id AND v.attribute_def_id = @attribute_def_id
    AND v.component_id = e.key::uuid AND v.value <> e.value
RETURNING v.component_id;
