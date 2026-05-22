-- +goose Up
-- +goose StatementBegin
CREATE TABLE webhook_deliveries_local (
    id UUID CODEC (ZSTD(1)),
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    team_id UUID CODEC (ZSTD(1)),
    webhook_id UUID CODEC (ZSTD(1)),
    event_id UUID CODEC (ZSTD(1)),
    sandbox_id String CODEC (ZSTD(1)),
    event_type LowCardinality(String) CODEC (ZSTD(1)),
    delivery_status LowCardinality(String) CODEC (ZSTD(1)),
    duration_ms UInt32 CODEC (ZSTD(1)),
    request_body String CODEC (ZSTD(1)),
    request_headers String CODEC (ZSTD(1)),
    request_url String CODEC (ZSTD(1)),
    response_body Nullable(String) CODEC (ZSTD(1)),
    response_headers Nullable(String) CODEC (ZSTD(1)),
    response_http_status_code Nullable(UInt16) CODEC (ZSTD(1)),
    error_class LowCardinality(String) CODEC (ZSTD(1)),
    error_message Nullable(String) CODEC (ZSTD(1))
) ENGINE = MergeTree
    PARTITION BY toDate(timestamp)
    ORDER BY (team_id, webhook_id, timestamp, id)
    TTL toDateTime(timestamp) + INTERVAL 7 DAY
    SETTINGS ttl_only_drop_parts = 1;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE webhook_deliveries AS webhook_deliveries_local
    ENGINE = Distributed('cluster', currentDatabase(), 'webhook_deliveries_local', xxHash64(team_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_deliveries;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_deliveries_local;
-- +goose StatementEnd
