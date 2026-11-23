-- +goose Up

-- Autovacuum analyzes a table once a tenth of its rows have changed, by
-- default, which a new project in a large instance never reaches. Until
-- something analyzes its tables, PostgreSQL estimates the project at about one
-- row in each, and picks nested loops that read every row of the workspace's
-- projects for each row of this one: reads of a whole project that take
-- milliseconds then run for more than ten seconds.
--
-- Each table with a project_id is analyzed after 50 changed rows, however large
-- it is, so that a project is planned by its real size within a pass of
-- autovacuum, a minute by default. ANALYZE samples a fixed number of rows, so
-- its cost does not grow with the table. Setting these takes a lock that
-- neither reads nor writes wait for, and rewrites nothing. TestTenantTables
-- holds a new table with a project_id to the same settings.
ALTER TABLE api_token SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE applied_command SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE asset SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE attribute_def SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE attribute_value SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE authoring_export SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE board SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE branch_output SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE comment SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE component SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE component_type SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE connection SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE expression SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE expression_name SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE expression_ref SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE folder SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE idempotency_record SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE node SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE notification SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE operation SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE play_state SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE project_grant SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE schema_migration_log SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE search_entry SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE search_state SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE snapshot SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE text_statistic SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE text_statistic_state SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE validation_mute SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE variable SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
