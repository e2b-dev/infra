-- name: GetTeamWithTierByTeamAndUser :one
SELECT sqlc.embed(t), sqlc.embed(tl)
FROM "public"."teams" t
JOIN "public"."users_teams" ut ON ut.team_id = t.id
JOIN "public"."team_limits" tl on tl.id = t.id
WHERE ut.user_id = $1 AND t.id = $2;

-- name: GetTeamsWithUsersTeamsWithTier :many
SELECT sqlc.embed(t), sqlc.embed(ut), sqlc.embed(tl)
FROM "public"."teams" t
JOIN "public"."users_teams" ut ON ut.team_id = t.id
JOIN "public"."team_limits" tl on tl.id = t.id
WHERE ut.user_id = $1;

-- name: GetTeamWithTierByAPIKeyWithUpdateLastUsed :one
UPDATE "public"."team_api_keys" tak
SET last_used = now()
FROM "public"."teams" t
JOIN "public"."team_limits" tl on tl.id = t.id
WHERE tak.team_id = t.id
  AND tak.api_key_hash = $1
RETURNING sqlc.embed(t), sqlc.embed(tl);