-- name: GetAuthUserByID :one
SELECT id, COALESCE(email, '') AS email, created_at
FROM auth.users
WHERE id = $1::uuid;
