BEGIN;

-- Add new columns to team_api_keys table
ALTER TABLE team_api_keys
    ADD COLUMN api_key_hash TEXT UNIQUE,
    ADD COLUMN api_key_mask VARCHAR(44);

-- Add new columns to access_tokens table
ALTER TABLE access_tokens
    ADD COLUMN id UUID DEFAULT gen_random_uuid(),
    ADD COLUMN access_token_hash TEXT UNIQUE,
    ADD COLUMN access_token_mask TEXT,
    ADD COLUMN name TEXT NOT NULL DEFAULT 'Unnamed Access Token';

-- Mark sensitive columns as sensitive
COMMENT ON COLUMN team_api_keys.api_key_hash IS 'sensitive';
COMMENT ON COLUMN access_tokens.access_token_hash IS 'sensitive';

COMMIT; 