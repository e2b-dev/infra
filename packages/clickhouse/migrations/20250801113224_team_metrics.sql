-- +goose Up

-- Create target table
CREATE TABLE team_metrics_gauge_local (
   timestamp     DateTime64(9) CODEC (ZSTD(1)),
   team_id       String        CODEC (ZSTD(1)),
   metric_name   LowCardinality(String) CODEC (ZSTD(1)),
   value         Float64       CODEC (ZSTD(1))
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (team_id, metric_name, toUnixTimestamp64Nano(timestamp))
TTL toDateTime(timestamp) + INTERVAL 30 DAY;

-- Create routing table that routes sandbox metrics
CREATE TABLE team_metrics_gauge AS team_metrics_gauge_local
    ENGINE = Distributed('cluster', currentDatabase(), 'team_metrics_gauge_local', xxHash64(team_id));

-- Create materialized view that routes sandbox metrics
CREATE MATERIALIZED VIEW team_metrics_gauge_mv
TO team_metrics_gauge AS SELECT
    toDateTime64(TimeUnix, 9) AS timestamp,
    Attributes['team_id'] AS team_id,
    MetricName AS metric_name,
    Value AS value
FROM metrics_gauge
WHERE MetricName LIKE 'e2b.team.%';


-- +goose Down
DROP TABLE IF EXISTS team_metrics_gauge_mv;
DROP TABLE IF EXISTS team_metrics_gauge;
DROP TABLE IF EXISTS team_metrics_gauge_local;
