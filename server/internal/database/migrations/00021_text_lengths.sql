-- +goose Up

-- A board's name and description, a folder's name, and a project's name and
-- description are held to the lengths that their commands' payloads take, as
-- a variable's description is (00020): at most 100 characters for a name, and
-- 2,000 for a description (packages board and project, MaxNameLength and
-- MaxDescriptionLength). The undo of a change of one writes the stored one
-- back, with a step that the payload's schema checks as the change is applied
-- (package commands), so that a longer one, as a row that the server did not
-- write may hold, would otherwise refuse every change of it. The other text
-- rules, which a later change of Go's Unicode tables may tighten, are not
-- checked of that step (commands.Spec.CheckNew). Every row that the server
-- wrote meets these already.
ALTER TABLE board
	ADD CONSTRAINT board_name_length CHECK (length(name) <= 100),
	ADD CONSTRAINT board_description_length CHECK (length(description) <= 2000);
ALTER TABLE folder ADD CONSTRAINT folder_name_length CHECK (length(name) <= 100);
ALTER TABLE project
	ADD CONSTRAINT project_name_length CHECK (length(name) <= 100),
	ADD CONSTRAINT project_description_length CHECK (length(description) <= 2000);
