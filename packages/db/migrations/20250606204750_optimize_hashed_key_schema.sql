-- +goose Up
-- +goose StatementBegin

-- Add new columns to team_api_keys table
ALTER TABLE team_api_keys
    ADD COLUMN IF NOT EXISTS api_key_prefix VARCHAR(44),
    ADD COLUMN IF NOT EXISTS api_key_length INTEGER,
    ADD COLUMN IF NOT EXISTS api_key_mask_prefix VARCHAR(44),
    ADD COLUMN IF NOT EXISTS api_key_mask_suffix VARCHAR(44);

-- Add new columns to access_tokens table
ALTER TABLE access_tokens
    ADD COLUMN IF NOT EXISTS access_token_prefix TEXT,
    ADD COLUMN IF NOT EXISTS access_token_length INTEGER,
    ADD COLUMN IF NOT EXISTS access_token_mask_prefix TEXT,
    ADD COLUMN IF NOT EXISTS access_token_mask_suffix TEXT;


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Remove the added columns from team_api_keys table
ALTER TABLE team_api_keys
DROP COLUMN IF EXISTS api_key_prefix,
    DROP COLUMN IF EXISTS api_key_length,
    DROP COLUMN IF EXISTS api_key_mask_prefix,
    DROP COLUMN IF EXISTS api_key_mask_suffix;

-- Remove the added columns from access_tokens table
ALTER TABLE access_tokens
DROP COLUMN IF EXISTS access_token_prefix,
    DROP COLUMN IF EXISTS access_token_length,
    DROP COLUMN IF EXISTS access_token_mask_prefix,
    DROP COLUMN IF EXISTS access_token_mask_suffix;

-- +goose StatementEnd
