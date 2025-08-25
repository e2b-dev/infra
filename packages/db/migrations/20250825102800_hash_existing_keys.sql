-- +goose Up

-- Function to convert hex to bytes and calculate SHA256 hash
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION hex_to_sha256(hex_str text, prefix text) RETURNS text AS $$
DECLARE
    bytes bytea;
    base64_hash text;
BEGIN
    -- Remove the prefix and convert remaining hex to bytes
    bytes := decode(substring(hex_str from length(prefix) + 1), 'hex');
    -- Get base64 hash and remove padding
    base64_hash := rtrim(encode(sha256(bytes), 'base64'), '=');
    -- Return SHA256 hash with $sha256$ prefix and base64-encoded hash without padding
    RETURN '$sha256$' || base64_hash;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Update existing API keys with hash and related fields
UPDATE public.team_api_keys
SET
    api_key_hash = hex_to_sha256(api_key, 'e2b_'),
    api_key_prefix = 'e2b_',
    api_key_length = length(api_key) - 4,  -- Strip 'e2b_' prefix (4 chars)
    api_key_mask_prefix = substring(api_key from 5 for 2),  -- Skip 'e2b_' prefix
    api_key_mask_suffix = substring(api_key from length(api_key) - 3)
WHERE
    api_key IS NOT NULL
    AND (api_key_hash IS NULL OR api_key_prefix IS NULL 
         OR api_key_length IS NULL OR api_key_mask_prefix IS NULL OR api_key_mask_suffix IS NULL);

-- Update existing access tokens with hash and related fields
UPDATE public.access_tokens
SET
    access_token_hash = hex_to_sha256(access_token, 'sk_e2b_'),
    access_token_prefix = 'sk_e2b_',
    access_token_length = length(access_token) - 7,  -- Strip 'sk_e2b_' prefix (7 chars)
    access_token_mask_prefix = substring(access_token from 8 for 2),  -- Skip 'sk_e2b_' prefix
    access_token_mask_suffix = substring(access_token from length(access_token) - 3)
WHERE
    access_token IS NOT NULL
    AND (access_token_hash IS NULL OR access_token_prefix IS NULL 
         OR access_token_length IS NULL OR access_token_mask_prefix IS NULL OR access_token_mask_suffix IS NULL);

-- Drop the helper functions as they are no longer needed
DROP FUNCTION hex_to_sha256(text, text);

-- +goose Down

-- Clear the computed fields (cannot reverse the hash computation)
UPDATE public.team_api_keys 
SET 
    api_key_hash = NULL,
    api_key_prefix = NULL,
    api_key_length = NULL,
    api_key_mask_prefix = NULL,
    api_key_mask_suffix = NULL
WHERE api_key_hash IS NOT NULL;

UPDATE public.access_tokens 
SET 
    access_token_hash = NULL,
    access_token_prefix = NULL,
    access_token_length = NULL,
    access_token_mask_prefix = NULL,
    access_token_mask_suffix = NULL
WHERE access_token_hash IS NOT NULL;
