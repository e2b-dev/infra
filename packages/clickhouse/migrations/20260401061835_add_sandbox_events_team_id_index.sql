-- +goose Up
ALTER TABLE sandbox_events_local
    ADD INDEX IF NOT EXISTS idx_team_id sandbox_team_id TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE sandbox_events_local
    MATERIALIZE INDEX idx_team_id;

-- +goose Down
ALTER TABLE sandbox_events_local
    DROP INDEX IF EXISTS idx_team_id;
