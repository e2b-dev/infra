-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_events
    ADD COLUMN type LowCardinality(String) CODEC (ZSTD(1)) AFTER event_label,
    ADD COLUMN version String CODEC (ZSTD(1)) AFTER type;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_events
DROP COLUMN IF EXISTS version,
DROP COLUMN IF EXISTS type;
-- +goose StatementEnd
