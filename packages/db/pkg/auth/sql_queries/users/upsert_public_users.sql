-- Bulk form of UpsertPublicUser: anchors rows so memberships referencing them
-- satisfy the foreign keys on users_teams.
-- name: UpsertPublicUsers :exec
INSERT INTO public.users (id)
SELECT candidate FROM unnest(sqlc.arg(ids)::uuid[]) AS candidate
ON CONFLICT (id) DO NOTHING;
