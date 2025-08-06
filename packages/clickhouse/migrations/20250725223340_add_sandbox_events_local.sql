-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_events_local (
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    sandbox_id String CODEC (ZSTD(1)),
    sandbox_execution_id String CODEC (ZSTD(1)),
    sandbox_template_id String CODEC (ZSTD(1)),
    sandbox_build_id String CODEC (ZSTD(1)),
    sandbox_team_id UUID CODEC (ZSTD(1)),
    event_category LowCardinality(String) CODEC (ZSTD(1)),
    event_label LowCardinality(String) CODEC (ZSTD(1)),
    event_data Nullable(String) CODEC (ZSTD(1))
) ENGINE = MergeTree 
    PARTITION BY toDate(timestamp)
    ORDER BY (sandbox_id, timestamp)
    TTL toDateTime(timestamp) + INTERVAL 7 DAY;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_events_local;
-- +goose StatementEnd
