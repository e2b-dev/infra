-- Creates the service_logs table for internal (platform service) logs written
-- by the OTel collectors' clickhouse exporter (terraform:
-- enable_clickhouse_logs, logs_table_name: service_logs).
--
-- The column set, indexes, partitioning and sort key mirror the exporter's own
-- create_schema DDL for opentelemetry-collector-contrib v0.146.0 exactly
-- (exporter/clickhouseexporter/internal/sqltemplates/logs_table.sql), because
-- the exporter INSERTs a fixed column list against this table with
-- create_schema disabled. The exporter introspects optional columns (EventName)
-- at startup, so keeping the full set is both required and forward-compatible.
--
-- On top of the upstream DDL this migration owns what create_schema never set:
-- a retention TTL and the Distributed wrapper (the cluster name is rewritten
-- by the migrator job on deployments that use a different cluster name).
--
-- ClickHouse has no transactional DDL and goose records the version only after
-- every statement succeeds, so each statement is written to be safely
-- re-runnable. The DROPs remove any leftover plain MergeTree otel_logs (the
-- exporter's default table name) previously created by a collector running
-- with create_schema enabled (staging validation data, intentionally
-- discarded), plus any partially created service_logs from a failed attempt.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS otel_logs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS service_logs;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS service_logs_local (
    `Timestamp` DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    `TimestampTime` DateTime DEFAULT toDateTime(Timestamp),
    `TraceId` String CODEC(ZSTD(1)),
    `SpanId` String CODEC(ZSTD(1)),
    `TraceFlags` UInt8,
    `SeverityText` LowCardinality(String) CODEC(ZSTD(1)),
    `SeverityNumber` UInt8,
    `ServiceName` LowCardinality(String) CODEC(ZSTD(1)),
    `Body` String CODEC(ZSTD(1)),
    `ResourceSchemaUrl` LowCardinality(String) CODEC(ZSTD(1)),
    `ResourceAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `ScopeSchemaUrl` LowCardinality(String) CODEC(ZSTD(1)),
    `ScopeName` String CODEC(ZSTD(1)),
    `ScopeVersion` LowCardinality(String) CODEC(ZSTD(1)),
    `ScopeAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `LogAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `EventName` String CODEC(ZSTD(1)),
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_log_attr_value mapValues(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_body Body TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 8
) ENGINE = MergeTree
    PARTITION BY toDate(TimestampTime)
    PRIMARY KEY (ServiceName, TimestampTime)
    ORDER BY (ServiceName, TimestampTime, Timestamp)
    TTL TimestampTime + toIntervalDay(30)
    SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
-- +goose StatementEnd

-- +goose StatementBegin
-- Internal logs carry no tenant key, so rows are spread randomly; queries
-- filter on ServiceName/time and fan out to all shards either way.
CREATE TABLE IF NOT EXISTS service_logs AS service_logs_local
    ENGINE = Distributed('cluster', currentDatabase(), 'service_logs_local', rand());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS service_logs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS service_logs_local;
-- +goose StatementEnd
