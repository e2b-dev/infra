-- name: UpsertPublicUser :exec
INSERT INTO public.users (id, email)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(email)::text)
ON CONFLICT (id)
DO UPDATE SET
    email = EXCLUDED.email,
    updated_at = now();
