-- +goose Up
-- TEMPORARY: Add a projection ordered by (sandbox_team_id, sandbox_id, timestamp)
-- so that queries filtering by sandbox_team_id can skip the full table scan.
-- The base ORDER BY is (sandbox_id, timestamp) which doesn't help when only
-- sandbox_team_id is provided.
-- This projection should be removed once we migrate the table's ORDER BY to
-- (sandbox_team_id, sandbox_id, timestamp) via a CREATE + RENAME swap.
ALTER TABLE sandbox_events_local
    ADD PROJECTION IF NOT EXISTS proj_team_id (
        SELECT * ORDER BY sandbox_team_id, sandbox_id, timestamp
    );

ALTER TABLE sandbox_events_local
    MATERIALIZE PROJECTION proj_team_id;

-- +goose Down
ALTER TABLE sandbox_events_local
    DROP PROJECTION IF EXISTS proj_team_id;
