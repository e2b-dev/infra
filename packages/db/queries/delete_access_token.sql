-- name: DeleteAccessToken :one
DELETE FROM "public"."access_tokens"
WHERE id = $1 AND user_id = $2
RETURNING id;
