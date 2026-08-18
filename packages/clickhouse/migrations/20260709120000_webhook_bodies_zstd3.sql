-- +goose Up
-- Webhook bodies/headers are large, highly compressible JSON payloads.
-- ZSTD(3) trades a little more CPU for a meaningfully smaller on-disk
-- footprint than ZSTD(1). Codec-only MODIFY COLUMN (no type/default repeated)
-- avoids drift with the original column definitions.
ALTER TABLE webhook_deliveries_local
    MODIFY COLUMN request_body CODEC (ZSTD(3)),
    MODIFY COLUMN request_headers CODEC (ZSTD(3)),
    MODIFY COLUMN response_body CODEC (ZSTD(3)),
    MODIFY COLUMN response_headers CODEC (ZSTD(3));

-- +goose Down
ALTER TABLE webhook_deliveries_local
    MODIFY COLUMN request_body CODEC (ZSTD(1)),
    MODIFY COLUMN request_headers CODEC (ZSTD(1)),
    MODIFY COLUMN response_body CODEC (ZSTD(1)),
    MODIFY COLUMN response_headers CODEC (ZSTD(1));
