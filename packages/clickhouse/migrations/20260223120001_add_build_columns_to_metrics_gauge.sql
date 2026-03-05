-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge_local
    ADD COLUMN IF NOT EXISTS build_id String DEFAULT '' CODEC (ZSTD(1)),
    ADD COLUMN IF NOT EXISTS sandbox_type LowCardinality(String) DEFAULT 'sandbox' CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge
    ADD COLUMN IF NOT EXISTS build_id String DEFAULT '' CODEC (ZSTD(1)),
    ADD COLUMN IF NOT EXISTS sandbox_type LowCardinality(String) DEFAULT 'sandbox' CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge_mv MODIFY QUERY
SELECT
    toDateTime64(TimeUnix, 9) AS timestamp,
    Attributes['sandbox_id'] AS sandbox_id,
    Attributes['team_id'] AS team_id,
    Attributes['build_id'] AS build_id,
    Attributes['sandbox_type'] AS sandbox_type,
    MetricName AS metric_name,
    Value AS value
FROM metrics_gauge
WHERE MetricName LIKE 'e2b.sandbox.%';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge_mv MODIFY QUERY
SELECT
    toDateTime64(TimeUnix, 9) AS timestamp,
    Attributes['sandbox_id'] AS sandbox_id,
    Attributes['team_id'] AS team_id,
    MetricName AS metric_name,
    Value AS value
FROM metrics_gauge
WHERE MetricName LIKE 'e2b.sandbox.%';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge_local
    DROP COLUMN IF EXISTS build_id,
    DROP COLUMN IF EXISTS sandbox_type;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge
    DROP COLUMN IF EXISTS build_id,
    DROP COLUMN IF EXISTS sandbox_type;
-- +goose StatementEnd
