-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_events
    ADD COLUMN event LowCardinality(String) CODEC (ZSTD(1)) AFTER event_label,
    ADD COLUMN version String CODEC (ZSTD(1)) AFTER event;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_events
DROP COLUMN IF EXISTS version,
DROP COLUMN IF EXISTS event;
-- +goose StatementEnd
