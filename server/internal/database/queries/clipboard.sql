-- name: ClipboardVariables :many
-- ClipboardVariables returns the project's variables that are not in its
-- trash whose IDs are among ids, in the order of their IDs, with what a
-- clipboard declares of each (package clipboard).
SELECT id, key, scope, board_id, type, default_value, description, reset_on_enter FROM live_variable
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
ORDER BY id;

-- name: ClipboardComponents :many
-- ClipboardComponents returns the project's components that are not in its
-- trash whose IDs are among ids, in the order of their IDs, each with its
-- type's ID, key, and name, with what a clipboard declares of each.
SELECT c.id, c.key, c.name, t.id AS type_id, t.key AS type_key, t.name AS type_name
FROM live_component c
JOIN component_type t ON t.workspace_id = c.workspace_id AND t.project_id = c.project_id
    AND t.id = c.component_type_id
WHERE c.workspace_id = @workspace_id AND c.project_id = @project_id AND c.id = ANY(@ids::uuid[])
ORDER BY c.id;

-- name: ClipboardAttributes :many
-- ClipboardAttributes returns the project's attribute definitions that are
-- not in its trash whose IDs are among ids, in the order of their IDs, with
-- what a clipboard declares of each that an expression does not read: its
-- name and its default, which creating one in its place takes.
SELECT id, name, default_value FROM live_attribute_def
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
ORDER BY id;

-- name: ClipboardComponentTypes :many
-- ClipboardComponentTypes returns the project's component types that are not
-- in its trash, in the order of their IDs, each with its key, by which a
-- paste resolves a component's type.
SELECT id, key FROM live_component_type
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY id;

-- name: ClipboardSpeakers :many
-- ClipboardSpeakers returns the IDs of the project's components, in its trash
-- or not, whose IDs are among ids, in their order: the speakers of a
-- clipboard's elements that the project has, which a paste keeps where they
-- resolve to nothing, as node.create takes a speaker of either.
SELECT id FROM component
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
ORDER BY id;

-- name: ClipboardCovers :many
-- ClipboardCovers returns the IDs of the project's assets whose IDs are among
-- ids, in their order: the covers of a clipboard's elements that the project
-- has, which a paste keeps, as node.create takes a cover of the project's.
SELECT id FROM asset
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = ANY(@ids::uuid[])
ORDER BY id;
