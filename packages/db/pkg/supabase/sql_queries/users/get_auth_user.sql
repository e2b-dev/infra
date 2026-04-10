-- name: GetAuthUserByID :one
SELECT id, COALESCE(email, '') AS email
FROM auth.users
WHERE id = $1::uuid;
