-- name: Test_DeleteAccessToken :exec
DELETE FROM "public"."access_tokens"
WHERE user_id = $1;
