-- name: GetUserByEmail :one
SELECT id
FROM public.users
WHERE email = sqlc.arg(email)::text;
