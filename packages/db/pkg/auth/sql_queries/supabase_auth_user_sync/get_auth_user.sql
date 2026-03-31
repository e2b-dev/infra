-- name: GetAuthUserByID :one
SELECT id, email
FROM auth.users
WHERE id = sqlc.arg(user_id)::uuid;
