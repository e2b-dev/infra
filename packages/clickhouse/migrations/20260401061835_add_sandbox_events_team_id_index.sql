-- +goose Up
ALTER TABLE sandbox_events_local
    ADD INDEX idx_team_id sandbox_team_id TYPE bloom_filter GRANULARITY 4;

ALTER TABLE sandbox_events_local
    MATERIALIZE INDEX idx_team_id;

-- +goose Down
ALTER TABLE sandbox_events_local
    DROP INDEX IF EXISTS idx_team_id;
