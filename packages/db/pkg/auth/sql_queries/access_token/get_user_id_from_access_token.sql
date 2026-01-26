-- name: GetUserIDFromAccessToken :one
SELECT user_id  FROM access_tokens
WHERE access_token_hash = @hashedToken;
