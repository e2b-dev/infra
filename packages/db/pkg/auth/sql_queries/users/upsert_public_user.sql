-- name: UpsertPublicUser :exec
INSERT INTO public.users (id)
VALUES (sqlc.arg(id)::uuid)
ON CONFLICT (id)
DO UPDATE SET
    updated_at = now();
