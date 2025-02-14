-- Function to convert hex to bytes and calculate SHA256 hash
CREATE OR REPLACE FUNCTION hex_to_sha256(hex_str text, prefix text) RETURNS text AS $$
DECLARE
    bytes bytea;
BEGIN
    -- Remove the prefix and convert remaining hex to bytes
    bytes := decode(substring(hex_str from length(prefix) + 1), 'hex');
    -- Return SHA256 hash as base64 string
    RETURN encode(sha256(bytes), 'base64');
END;
$$ LANGUAGE plpgsql;

-- Function to create mask for keys
CREATE OR REPLACE FUNCTION create_key_mask(key_str text, prefix text) RETURNS text AS $$
DECLARE
    key_length int;
    visible_chars text;
BEGIN
    -- Get the last 4 characters
    visible_chars := substring(key_str from length(key_str) - 3);
    -- Calculate how many asterisks we need (length without prefix and without last 4 chars)
    key_length := length(key_str) - length(prefix) - 4;
    -- Construct mask: prefix + asterisks + last 4 chars
    RETURN prefix || repeat('*', key_length) || visible_chars;
END;
$$ LANGUAGE plpgsql;

-- Update existing API keys with hash and mask
UPDATE public.team_api_keys
SET 
    api_key_hash = hex_to_sha256(api_key, 'e2b_'),
    api_key_mask = create_key_mask(api_key, 'e2b_')
WHERE 
    api_key IS NOT NULL 
    AND (api_key_hash IS NULL OR api_key_mask IS NULL);

-- Update existing access tokens with hash and mask
UPDATE public.access_tokens
SET 
    access_token_hash = hex_to_sha256(access_token, 'sk_e2b_'),
    access_token_mask = create_key_mask(access_token, 'sk_e2b_')
WHERE 
    access_token IS NOT NULL 
    AND (access_token_hash IS NULL OR access_token_mask IS NULL);

-- Drop the helper functions as they are no longer needed
DROP FUNCTION hex_to_sha256(text, text);
DROP FUNCTION create_key_mask(text, text);
