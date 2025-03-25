-- name: TeamsListForUser :many
SELECT sqlc.embed(t), sqlc.embed(tak), sqlc.embed(ut)
FROM "public"."teams" t
LEFT JOIN "public"."team_api_keys" tak ON t.id = tak.team_id
LEFT JOIN "public"."users_teams" ut ON ut.team_id = t.id and ut.user_id = $1;
