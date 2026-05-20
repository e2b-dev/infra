-- name: GetTeamMembers :many
SELECT
    ut.user_id,
    ut.team_id,
    ut.is_default,
    ut.added_by,
    ut.created_at
FROM public.users_teams ut
WHERE ut.team_id = sqlc.arg(team_id)::uuid;

-- name: GetTeamMemberRelation :one
SELECT * FROM public.users_teams
WHERE team_id = sqlc.arg(team_id)::uuid
  AND user_id = sqlc.arg(user_id)::uuid;

-- name: LockTeamMembersForUpdate :many
SELECT user_id FROM public.users_teams
WHERE team_id = sqlc.arg(team_id)::uuid
FOR UPDATE;

-- name: GetPublicUserID :one
SELECT id FROM public.users
WHERE id = sqlc.arg(id)::uuid;

-- name: AddTeamMember :exec
INSERT INTO public.users_teams (user_id, team_id, is_default, added_by)
VALUES (
    sqlc.arg(user_id)::uuid,
    sqlc.arg(team_id)::uuid,
    false,
    sqlc.arg(added_by)::uuid
);

-- name: RemoveTeamMember :exec
DELETE FROM public.users_teams
WHERE team_id = sqlc.arg(team_id)::uuid
  AND user_id = sqlc.arg(user_id)::uuid;
