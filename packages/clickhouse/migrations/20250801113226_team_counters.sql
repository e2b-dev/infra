-- +goose Up

CREATE TABLE IF NOT EXISTS "metrics_sum" (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    ResourceSchemaUrl String CODEC(ZSTD(1)),
    ScopeName String CODEC(ZSTD(1)),
    ScopeVersion String CODEC(ZSTD(1)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    ScopeDroppedAttrCount UInt32 CODEC(ZSTD(1)),
    ScopeSchemaUrl String CODEC(ZSTD(1)),
    ServiceName LowCardinality(String) CODEC(ZSTD(1)),
    MetricName String CODEC(ZSTD(1)),
    MetricDescription String CODEC(ZSTD(1)),
    MetricUnit String CODEC(ZSTD(1)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    StartTimeUnix DateTime64(9) CODEC(Delta, ZSTD(1)),
    TimeUnix DateTime64(9) CODEC(Delta, ZSTD(1)),
    Value Float64 CODEC(ZSTD(1)),
    Flags UInt32  CODEC(ZSTD(1)),
    Exemplars Nested (
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime64(9),
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC(ZSTD(1)),
    AggregationTemporality Int32 CODEC(ZSTD(1)),
    IsMonotonic Boolean CODEC(Delta, ZSTD(1))
) ENGINE = Null;


CREATE TABLE team_metrics_sum_local (
  timestamp     DateTime64(9) CODEC (ZSTD(1)),
  team_id       String        CODEC (ZSTD(1)),
  metric_name   LowCardinality(String) CODEC (ZSTD(1)),
  value         Float64       CODEC (ZSTD(1))
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (team_id, metric_name, toUnixTimestamp64Nano(timestamp))
TTL toDateTime(timestamp) + INTERVAL 30 DAY;

CREATE TABLE team_metrics_sum AS team_metrics_sum_local
    ENGINE = Distributed('cluster', currentDatabase(), 'team_metrics_sum_local', xxHash64(team_id));


CREATE MATERIALIZED VIEW team_metrics_sum_mv
TO team_metrics_sum AS SELECT
 toDateTime64(TimeUnix, 9) AS timestamp,
 Attributes['team_id'] AS team_id,
 MetricName AS metric_name,
 Value AS value
FROM metrics_sum
WHERE MetricName LIKE 'e2b.team.%';

-- +goose Down
DROP TABLE IF EXISTS "metrics_sum";
DROP TABLE IF EXISTS team_metrics_sum;
DROP TABLE IF EXISTS team_metrics_sum_local;
DROP TABLE IF EXISTS team_metrics_sum_mv;