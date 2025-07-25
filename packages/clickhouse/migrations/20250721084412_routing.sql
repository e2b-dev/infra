-- +goose Up

-- 1. Drop old sharding table
DROP TABLE IF EXISTS metrics_gauge;

-- 2. Create a dummy routing table with Null engine
CREATE TABLE metrics_gauge (
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
        FilteredAttributes    Map(LowCardinality(String), String),
        TimeUnix              DateTime64(9),
        Value                 Float64,
        SpanId                String,
        TraceId               String
   ) CODEC (ZSTD(1))
) ENGINE = Null;

-- 3. Drop the old source table
DROP TABLE IF EXISTS metrics_gauge_local;

-- +goose Down

-- 1. Drop the Null-engine routing table
DROP TABLE IF EXISTS metrics_gauge;

-- 2. Recreate the original sharding table
CREATE TABLE metrics_gauge as metrics_gauge_local
    ENGINE = Distributed('cluster', currentDatabase(), 'metrics_gauge_local', xxHash64(Attributes));

-- 3. Recreate the original source table
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
        FilteredAttributes    Map(LowCardinality(String), String),
        TimeUnix              DateTime64(9),
        Value                 Float64,
        SpanId                String,
        TraceId               String
    ) CODEC (ZSTD(1)),
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
