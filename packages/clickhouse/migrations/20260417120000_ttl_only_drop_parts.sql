-- +goose Up
-- Enable ttl_only_drop_parts on all local MergeTree tables.
-- Since all tables use PARTITION BY toDate(timestamp), TTL boundaries align
-- with part boundaries. This avoids expensive part rewrites during TTL
-- cleanup -- ClickHouse will drop entire parts instead.
ALTER TABLE sandbox_metrics_gauge_local MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE team_metrics_gauge_local MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE team_metrics_sum_local MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE sandbox_events_local MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE sandbox_host_stats_local MODIFY SETTING ttl_only_drop_parts = 1;

-- +goose Down
ALTER TABLE sandbox_metrics_gauge_local RESET SETTING ttl_only_drop_parts;
ALTER TABLE team_metrics_gauge_local RESET SETTING ttl_only_drop_parts;
ALTER TABLE team_metrics_sum_local RESET SETTING ttl_only_drop_parts;
ALTER TABLE sandbox_events_local RESET SETTING ttl_only_drop_parts;
ALTER TABLE sandbox_host_stats_local RESET SETTING ttl_only_drop_parts;
