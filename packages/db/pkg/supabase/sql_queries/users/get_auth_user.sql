-- name: GetAuthUserByID :one
SELECT id, COALESCE(email, '') AS email, created_at, COALESCE(raw_app_meta_data, '{}'::jsonb) AS raw_app_meta_data
FROM auth.users
WHERE id = $1::uuid;
