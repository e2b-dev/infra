-- +goose Up
-- +goose StatementBegin
CREATE TABLE metrics_local (
   timestamp DateTime('UTC'),
   sandbox_id String,
   team_id LowCardinality(String),
   cpu_count UInt32,
   cpu_used_pct Float32,
   mem_total_mib UInt64,
   mem_used_mib UInt64
) ENGINE = MergeTree()
 ORDER BY (team_id, sandbox_id, timestamp)
 PARTITION BY toDate(timestamp)
 TTL timestamp + INTERVAL 7 DAY;

CREATE TABLE metrics as metrics_local
    ENGINE = Distributed('cluster', currentDatabase(), 'metrics_local', xxHash64(sandbox_id));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics;
DROP TABLE IF EXISTS metrics_local;
-- +goose StatementEnd

