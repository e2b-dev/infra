-- name: GetTeamsWithUsersTeams :many
SELECT sqlc.embed(t), sqlc.embed(ut)
FROM "public"."teams" t
JOIN "public"."users_teams" ut ON ut.team_id = t.id
WHERE ut.user_id = $1;
