-- name: GetTeamWithTierByAPIKey :one
SELECT sqlc.embed(t), sqlc.embed(tl) FROM "public"."team_api_keys" tak
JOIN "public"."teams" t ON tak.team_id = t.id
JOIN "public"."team_limits" tl on tl.id = t.id
WHERE tak.team_id = t.id
  AND tak.api_key_hash = $1;

-- name: GetTeamWithTierByTeamAndUser :one
SELECT sqlc.embed(t), sqlc.embed(tl)
FROM "public"."teams" t
         JOIN "public"."users_teams" ut ON ut.team_id = t.id
         JOIN "public"."team_limits" tl on tl.id = t.id
WHERE ut.user_id = $1 AND t.id = $2;

-- name: GetTeamWithTierByTeamID :one
SELECT sqlc.embed(t), sqlc.embed(tl)
FROM "public"."teams" t
         JOIN "public"."team_limits" tl on tl.id = t.id
WHERE t.id = $1;

-- name: GetTeamsWithUsersTeamsWithTier :many
SELECT sqlc.embed(t), sqlc.embed(ut), sqlc.embed(tl)
FROM "public"."teams" t
         JOIN "public"."users_teams" ut ON ut.team_id = t.id
         JOIN "public"."team_limits" tl on tl.id = t.id
WHERE ut.user_id = $1;
