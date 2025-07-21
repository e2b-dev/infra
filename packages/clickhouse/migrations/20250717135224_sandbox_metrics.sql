-- +goose Up

-- Create target table
CREATE TABLE sandbox_metrics_gauge_local (
   timestamp     DateTime64(9) CODEC (ZSTD(1)),
   sandbox_id    String        CODEC (ZSTD(1)),
   team_id       String        CODEC (ZSTD(1)),
   metric_name   LowCardinality(String) CODEC (ZSTD(1)),
   value         Float64       CODEC (ZSTD(1))
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (sandbox_id, metric_name, toUnixTimestamp64Nano(timestamp))
TTL toDateTime(timestamp) + INTERVAL 7 DAY;

-- Create routing table that routes sandbox metrics
CREATE TABLE sandbox_metrics_gauge AS sandbox_metrics_gauge_local
    ENGINE = Distributed('cluster', currentDatabase(), 'sandbox_metrics_gauge_local', xxHash64(sandbox_id));

-- Create materialized view that routes sandbox metrics
CREATE MATERIALIZED VIEW sandbox_metrics_gauge_mv
TO sandbox_metrics_gauge AS SELECT
    toDateTime64(TimeUnix, 9) AS timestamp,
    Attributes['sandbox_id'] AS sandbox_id,
    Attributes['team_id'] AS team_id,
    MetricName AS metric_name,
    Value AS value
FROM metrics_gauge
WHERE Attributes['sandbox_id'] IS NOT NULL;

-- Insert existing sandbox metrics into the local table
INSERT INTO sandbox_metrics_gauge SELECT
  TimeUnix AS timestamp,
  Attributes['sandbox_id'] AS sandbox_id,
  Attributes['team_id'] AS team_id,
  MetricName AS metric_name,
  Value AS value
FROM metrics_gauge_local WHERE Attributes['sandbox_id'] IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS sandbox_metrics_gauge_mv;
DROP TABLE IF EXISTS sandbox_metrics_gauge;
DROP TABLE IF EXISTS sandbox_metrics_gauge_local;
