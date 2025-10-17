-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_events_local
    ADD COLUMN event LowCardinality(String) CODEC (ZSTD(1)) AFTER event_label,
    ADD COLUMN version String CODEC (ZSTD(1)) AFTER event;

ALTER TABLE sandbox_events
    ADD COLUMN event LowCardinality(String) CODEC (ZSTD(1)) AFTER event_label,
    ADD COLUMN version String CODEC (ZSTD(1)) AFTER event;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_events_local
DROP COLUMN IF EXISTS version,
DROP COLUMN IF EXISTS event;

ALTER TABLE sandbox_events
DROP COLUMN IF EXISTS version,
DROP COLUMN IF EXISTS event;
-- +goose StatementEnd
