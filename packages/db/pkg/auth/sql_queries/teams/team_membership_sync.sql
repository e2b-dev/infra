-- Membership reconciliation for the control-plane management interface. Unlike
-- the dashboard's member routes these enforce no team-side rules: the caller
-- owns membership, and a rule here would make its pushes unrepeatable.

-- name: TeamExists :one
SELECT EXISTS (
    SELECT 1 FROM public.teams WHERE id = sqlc.arg(team_id)::uuid
)::boolean;

-- name: SyncTeamMembersPresent :exec
INSERT INTO public.users_teams (user_id, team_id, is_default, added_by)
SELECT
    candidate,
    sqlc.arg(team_id)::uuid,
    false,
    sqlc.narg(added_by)::uuid
FROM unnest(sqlc.arg(user_ids)::uuid[]) AS candidate
ON CONFLICT (team_id, user_id) DO NOTHING;

-- Returns what it removed, because afterwards the rows are gone — the same
-- reason InvalidateTeamCache cannot find them either.
-- name: SyncTeamMembersAbsent :many
DELETE FROM public.users_teams
WHERE team_id = sqlc.arg(team_id)::uuid
  AND user_id = ANY(sqlc.arg(user_ids)::uuid[])
RETURNING user_id, is_default;
