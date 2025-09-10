-- name: CreateAccessToken :one
INSERT INTO "public"."access_tokens"(
    id,
    user_id,
    access_token_hash,
    access_token_prefix,
    access_token_length,
    access_token_mask_prefix,
    access_token_mask_suffix,
    name
)
    VALUES
(
    @id,
    @user_id,
    @access_token_hash,
    @access_token_prefix,
    @access_token_length,
    @access_token_mask_prefix,
    @access_token_mask_suffix,
@name
) RETURNING id, user_id, access_token_hash, access_token_prefix, access_token_length, access_token_mask_prefix, access_token_mask_suffix, name, created_at;