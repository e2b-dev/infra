-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_events_local (
    timestamp DateTime64(9),
    sandbox_id String,
    sandbox_execution_id String,
    sandbox_template_id String,
    sandbox_build_id String,
    sandbox_team_id UUID,
    event_category String,
    event_label String,
    event_data Nullable(String)
) ENGINE = MergeTree 
    PARTITION BY toDate(timestamp)
    ORDER BY (sandbox_id, timestamp)
    TTL toDateTime(timestamp) + INTERVAL 7 DAY;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_events_local;
-- +goose StatementEnd
