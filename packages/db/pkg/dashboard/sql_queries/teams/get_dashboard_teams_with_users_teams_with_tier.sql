-- name: GetDashboardTeamsWithUsersTeamsWithTier :many
SELECT sqlc.embed(t), ut.is_default, sqlc.embed(tl)
FROM "public"."teams" t
JOIN "public"."users_teams" ut ON ut.team_id = t.id
JOIN "public"."team_limits" tl ON tl.id = t.id
WHERE ut.user_id = sqlc.arg(user_id)::uuid;
