-- name: SetScope :exec
-- SetScope sets the workspace and the user that row-level security policies
-- compare against, until the end of the transaction, as SET LOCAL does.
SELECT
    set_config('app.workspace_id', @workspace_id::text, true),
    set_config('app.user_id', @user_id::text, true);
