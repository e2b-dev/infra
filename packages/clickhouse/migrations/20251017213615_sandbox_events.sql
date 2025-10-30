-- +goose Up

-- Update global events table
ALTER TABLE sandbox_events_local
    ADD COLUMN type LowCardinality(String) CODEC (ZSTD(1)) AFTER event_label,
    ADD COLUMN version String DEFAULT 'v1' CODEC (ZSTD(1)) AFTER type;

-- Update local events table
ALTER TABLE sandbox_events
    ADD COLUMN type LowCardinality(String) CODEC (ZSTD(1)) AFTER event_label,
    ADD COLUMN version String DEFAULT 'v1' CODEC (ZSTD(1)) AFTER type;

-- +goose Down
-- Remove columns from clustered table
ALTER TABLE sandbox_events
DROP COLUMN IF EXISTS version,
DROP COLUMN IF EXISTS type;

-- Remove columns from local table
ALTER TABLE sandbox_events_local
DROP COLUMN IF EXISTS version,
DROP COLUMN IF EXISTS type;
