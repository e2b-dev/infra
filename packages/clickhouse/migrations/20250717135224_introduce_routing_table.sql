-- +goose Up
-- +goose StatementBegin
-- 1. Rename old source table
RENAME TABLE metrics_gauge_local TO metrics_gauge_local_old;

-- 2. Create a dummy routing table with Null engine
CREATE TABLE metrics_gauge_local (
    ResourceAttributes    Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl     String CODEC (ZSTD(1)),
    ScopeName             String CODEC (ZSTD(1)),
    ScopeVersion          String CODEC (ZSTD(1)),
    ScopeAttributes       Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl        String CODEC (ZSTD(1)),
    ServiceName           LowCardinality(String) CODEC (ZSTD(1)),
    MetricName            LowCardinality(String) CODEC (ZSTD(1)),
    MetricDescription     String CODEC (ZSTD(1)),
    MetricUnit            String CODEC (ZSTD(1)),
    Attributes            Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix         DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix              DateTime64(9) CODEC (Delta, ZSTD(1)),
    Value                 Float64 CODEC (ZSTD(1)),
    Flags                 UInt32 CODEC (ZSTD(1)),
    Exemplars             Nested(
        FilteredAttributes Map(LowCardinality(String), String)),
        TimeUnix              DateTime64(9),
        Value                 Float64,
        SpanId                String,
        TraceId String
        ) CODEC (ZSTD(1))
    ),
) ENGINE = Null;

-- 3. Create target table
CREATE TABLE sandbox_metrics_gauge as metrics_gauge_local (
   timestamp     DateTime64(9) CODEC (ZSTD(1)),
   sandbox_id    String        CODEC (ZSTD(1)),
   team_id       String        CODEC (ZSTD(1)),
   metric_name   LowCardinality(String) CODEC (ZSTD(1)),
   value         Float64       CODEC (ZSTD(1))
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (sandbox_id, metric_name, toUnixTimestamp64Nano(timestamp))
TTL toDateTime(timestamp) + INTERVAL 7 DAY;

-- 4. Create materialized view that routes sandbox metrics
CREATE MATERIALIZED VIEW sandbox_metrics_gauge_mv
TO sandbox_metrics_gauge AS
SELECT
    toDateTime64(TimeUnix, 9) AS timestamp,
    Attributes['sandbox_id'] AS sandbox_id,
    Attributes['team_id'] AS team_id,
    MetricName AS metric_name,
    Value AS value
FROM metrics_gauge_local
WHERE Attributes['sandbox_id'] IS NOT NULL;

-- 5. Replay historical data through the routing table
INSERT INTO metrics_gauge_local SELECT * FROM metrics_gauge_local_old WHERE Attributes['sandbox_id'] IS NOT NULL;

-- 6. Drop the old staging table
DROP TABLE IF EXISTS metrics_gauge_local_old;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 1. Drop the materialized view (must be first to release dependency on source table)
DROP TABLE IF EXISTS sandbox_metrics_gauge_mv;

-- 2. Drop the Null-engine routing table
DROP TABLE IF EXISTS metrics_gauge;

-- 3. Rename original staging table back to its old name
CREATE TABLE metrics_gauge_local(
    ResourceAttributes    Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl     String CODEC (ZSTD(1)),
    ScopeName             String CODEC (ZSTD(1)),
    ScopeVersion          String CODEC (ZSTD(1)),
    ScopeAttributes       Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl        String CODEC (ZSTD(1)),
    ServiceName           LowCardinality(String) CODEC (ZSTD(1)),
    MetricName            LowCardinality(String) CODEC (ZSTD(1)),
    MetricDescription     String CODEC (ZSTD(1)),
    MetricUnit            String CODEC (ZSTD(1)),
    Attributes            Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix         DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix              DateTime64(9) CODEC (Delta, ZSTD(1)),
    Value                 Float64 CODEC (ZSTD(1)),
    Flags                 UInt32 CODEC (ZSTD(1)),
    Exemplars             Nested(
        FilteredAttributes Map(LowCardinality(String), String)),
        TimeUnix              DateTime64(9),
        Value                 Float64,
        SpanId                String,
        TraceId String
        ) CODEC (ZSTD(1))
    ),
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree
    PARTITION BY toDate(TimeUnix)
    ORDER BY (ServiceName, MetricName, Attributes, toUnixTimestamp64Nano(TimeUnix))
    TTL toDateTime(TimeUnix) + INTERVAL 7 DAY
    SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- 4. Drop the derived target table
DROP TABLE IF EXISTS sandbox_metrics_gauge;
-- +goose StatementEnd
