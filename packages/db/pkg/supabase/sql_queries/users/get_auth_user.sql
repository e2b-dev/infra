-- name: GetAuthUserByID :one
SELECT id, COALESCE(email, '') AS email, created_at, COALESCE(raw_app_meta_data, '{}'::jsonb) AS raw_app_meta_data
FROM auth.users
WHERE id = $1::uuid;

-- name: GetAuthUsersByIDs :many
SELECT id, COALESCE(email, '') AS email, created_at, COALESCE(raw_app_meta_data, '{}'::jsonb) AS raw_app_meta_data
FROM auth.users
WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: GetAuthUsersByEmail :many
SELECT id, COALESCE(email, '') AS email, created_at, COALESCE(raw_app_meta_data, '{}'::jsonb) AS raw_app_meta_data
FROM auth.users
WHERE email = sqlc.arg(email)::text;

-- name: SearchAuthUsersByEmail :many
SELECT id, COALESCE(email, '') AS email, created_at, COALESCE(raw_app_meta_data, '{}'::jsonb) AS raw_app_meta_data
FROM auth.users
WHERE email ILIKE '%' || sqlc.arg(query)::text || '%'
ORDER BY email
LIMIT sqlc.arg(result_limit)::int;

-- name: GetLatestAuthSessionByUserID :one
SELECT user_agent, ip
FROM auth.sessions
WHERE user_id = $1::uuid
ORDER BY created_at DESC
LIMIT 1;
