-- +goose Up

-- Reduce TTL for team metrics gauge table from 90 days to 7 days
ALTER TABLE team_metrics_gauge_local MODIFY TTL toDateTime(timestamp) + INTERVAL 7 DAY;

-- Reduce TTL for team metrics sum table from 90 days to 7 days
ALTER TABLE team_metrics_sum_local MODIFY TTL toDateTime(timestamp) + INTERVAL 7 DAY;

-- +goose Down
-- Revert TTL for team metrics gauge table back to 90 days
ALTER TABLE team_metrics_gauge_local MODIFY TTL toDateTime(timestamp) + INTERVAL 90 DAY;

-- Revert TTL for team metrics sum table back to 90 days
ALTER TABLE team_metrics_sum_local MODIFY TTL toDateTime(timestamp) + INTERVAL 90 DAY;
