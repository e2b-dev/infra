-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_host_stats_local (
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    sandbox_id String CODEC (ZSTD(1)),
    sandbox_execution_id String CODEC (ZSTD(1)),
    sandbox_template_id String CODEC (ZSTD(1)),
    sandbox_build_id String CODEC (ZSTD(1)),
    sandbox_team_id UUID CODEC (ZSTD(1)),
    sandbox_vcpu_count Int64 CODEC (ZSTD(1)),
    sandbox_memory_mb Int64 CODEC (ZSTD(1)),
    firecracker_cpu_user_time Float64 CODEC (ZSTD(1)),
    firecracker_cpu_system_time Float64 CODEC (ZSTD(1)),
    firecracker_memory_rss UInt64 CODEC (ZSTD(1)),
    firecracker_memory_vms UInt64 CODEC (ZSTD(1))
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (sandbox_id, timestamp)
TTL toDateTime(timestamp) + INTERVAL 7 DAY;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sandbox_host_stats AS sandbox_host_stats_local
    ENGINE = Distributed('cluster', currentDatabase(), 'sandbox_host_stats_local', xxHash64(sandbox_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_host_stats;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_host_stats_local;
-- +goose StatementEnd
