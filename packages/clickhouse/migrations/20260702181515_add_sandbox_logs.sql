-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_logs_local (
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    ingested_at DateTime64(9) DEFAULT now64(9) CODEC (Delta, ZSTD(1)),
    team_id UUID CODEC (ZSTD(1)),
    sandbox_id String CODEC (ZSTD(1)),
    template_id String CODEC (ZSTD(1)),
    build_id String CODEC (ZSTD(1)),
    service LowCardinality(String) CODEC (ZSTD(1)),
    category LowCardinality(String) CODEC (ZSTD(1)),
    level LowCardinality(String) CODEC (ZSTD(1)),
    message String CODEC (ZSTD(1)),
    raw String CODEC (ZSTD(1)),
    fields String CODEC (ZSTD(1)),
    INDEX idx_build_id build_id TYPE bloom_filter GRANULARITY 4,
    -- ngram bloom filter so the `position(message, ?) > 0` substring search used
    -- by the reader can be index-accelerated. tokenbf_v1 only supports whole-token
    -- (hasToken) lookups and would not help a substring predicate.
    INDEX idx_message_ngram message TYPE ngrambf_v1(4, 8192, 3, 0) GRANULARITY 4
) ENGINE = MergeTree
    PARTITION BY toDate(ingested_at)
    ORDER BY (team_id, sandbox_id, timestamp)
    TTL toDateTime(ingested_at) + INTERVAL 7 DAY
    SETTINGS ttl_only_drop_parts = 1;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sandbox_logs AS sandbox_logs_local
    ENGINE = Distributed('cluster', currentDatabase(), 'sandbox_logs_local', xxHash64(team_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_logs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_logs_local;
-- +goose StatementEnd
