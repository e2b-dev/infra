-- +goose Up
-- Retention of the event in days
ALTER TABLE sandbox_events_local
    ADD COLUMN IF NOT EXISTS events_ttl_days Int64 DEFAULT 7 CODEC (ZSTD(1));

ALTER TABLE sandbox_events
    ADD COLUMN IF NOT EXISTS events_ttl_days Int64 DEFAULT 7;

-- Switch the fixed 7-day table TTL to a per-row TTL driven by the team's retention
ALTER TABLE sandbox_events_local
    MODIFY TTL toDateTime(timestamp) + toIntervalDay(events_ttl_days);

-- Rows in this table now carry per-team TTLs, so a single daily part mixes
-- expiry times. ttl_only_drop_parts=1 (set globally in 20260417120000) would
-- keep the whole part alive until its max TTL passes. Turn it off for this
-- table only so TTL merges evict expired rows exactly, at the cost of part
-- rewrites instead of whole-part drops.
ALTER TABLE sandbox_events_local
    MODIFY SETTING ttl_only_drop_parts = 0;

-- +goose Down
ALTER TABLE sandbox_events_local
    MODIFY SETTING ttl_only_drop_parts = 1;

ALTER TABLE sandbox_events_local
    MODIFY TTL toDateTime(timestamp) + INTERVAL 7 DAY;

ALTER TABLE sandbox_events
    DROP COLUMN IF EXISTS events_ttl_days;

ALTER TABLE sandbox_events_local
    DROP COLUMN IF EXISTS events_ttl_days;
