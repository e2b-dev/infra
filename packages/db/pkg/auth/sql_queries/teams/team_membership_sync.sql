-- name: UpsertTeamMember :exec
INSERT INTO public.users_teams (user_id, team_id, is_default, added_by)
VALUES (
    sqlc.arg(user_id)::uuid,
    sqlc.arg(team_id)::uuid,
    false,
    sqlc.narg(added_by)::uuid
)
ON CONFLICT (team_id, user_id) DO NOTHING;

-- name: DeleteTeamMember :exec
DELETE FROM public.users_teams
WHERE team_id = sqlc.arg(team_id)::uuid
  AND user_id = sqlc.arg(user_id)::uuid;
