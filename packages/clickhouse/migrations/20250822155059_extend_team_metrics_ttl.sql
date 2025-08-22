-- +goose Up

-- Extend TTL for team metrics gauge table from 30 days to 90 days
ALTER TABLE team_metrics_gauge_local MODIFY TTL toDateTime(timestamp) + INTERVAL 90 DAY;

-- Extend TTL for team metrics sum table from 30 days to 90 days
ALTER TABLE team_metrics_sum_local MODIFY TTL toDateTime(timestamp) + INTERVAL 90 DAY;

-- +goose Down
-- Revert TTL for team metrics gauge table back to 30 days
ALTER TABLE team_metrics_gauge_local MODIFY TTL toDateTime(timestamp) + INTERVAL 30 DAY;

-- Revert TTL for team metrics sum table back to 30 days
ALTER TABLE team_metrics_sum_local MODIFY TTL toDateTime(timestamp) + INTERVAL 30 DAY;
