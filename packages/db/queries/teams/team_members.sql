-- name: GetTeamMembers :many
SELECT
    ut.user_id,
    ut.team_id,
    ut.is_default,
    ut.added_by,
    ut.created_at
FROM public.users_teams ut
WHERE ut.team_id = sqlc.arg(team_id)::uuid;
