-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
    ADD COLUMN IF NOT EXISTS sandbox_type LowCardinality(String) DEFAULT 'sandbox' CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats
    ADD COLUMN IF NOT EXISTS sandbox_type LowCardinality(String) DEFAULT 'sandbox' CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local DROP COLUMN IF EXISTS sandbox_type;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats DROP COLUMN IF EXISTS sandbox_type;
-- +goose StatementEnd
