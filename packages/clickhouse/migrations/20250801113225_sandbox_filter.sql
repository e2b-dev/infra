-- +goose Up

-- https://clickhouse.com/docs/sql-reference/statements/alter/view
ALTER TABLE sandbox_metrics_gauge_mv MODIFY QUERY
SELECT
    toDateTime64(TimeUnix, 9) AS timestamp,
    Attributes['sandbox_id'] AS sandbox_id,
    Attributes['team_id'] AS team_id,
    MetricName AS metric_name,
    Value AS value
FROM metrics_gauge
WHERE MetricName LIKE 'e2b.sandbox.%';


-- +goose Down
ALTER TABLE sandbox_metrics_gauge_mv MODIFY QUERY
SELECT
  TimeUnix AS timestamp,
  Attributes['sandbox_id'] AS sandbox_id,
  Attributes['team_id'] AS team_id,
  MetricName AS metric_name,
  Value AS value
FROM metrics_gauge_local WHERE Attributes['sandbox_id'] IS NOT NULL;