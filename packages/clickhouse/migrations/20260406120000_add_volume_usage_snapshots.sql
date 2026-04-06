-- +goose Up
-- +goose StatementBegin
CREATE TABLE volume_usage_snapshots_local (
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    team_id UUID CODEC (ZSTD(1)),
    volume_id UUID CODEC (ZSTD(1)),
    usage_bytes Int64 CODEC (ZSTD(1)),
    quota_bytes Int64 CODEC (ZSTD(1)),
    is_blocked UInt8 CODEC (ZSTD(1)),
    INDEX idx_team_id team_id TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (team_id, volume_id, timestamp)
TTL toDateTime(timestamp) + INTERVAL 30 DAY;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE volume_usage_snapshots AS volume_usage_snapshots_local
    ENGINE = Distributed('cluster', currentDatabase(), 'volume_usage_snapshots_local', xxHash64(concat(toString(team_id), toString(volume_id))));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS volume_usage_snapshots;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS volume_usage_snapshots_local;
-- +goose StatementEnd
