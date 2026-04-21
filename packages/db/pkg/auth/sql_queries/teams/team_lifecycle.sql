-- name: CreateTeam :one
INSERT INTO public.teams (name, tier, email, is_blocked, blocked_reason)
VALUES (
    sqlc.arg(name)::text,
    sqlc.arg(tier)::text,
    sqlc.arg(email)::text,
    sqlc.arg(is_blocked)::boolean,
    sqlc.narg(blocked_reason)::text
)
RETURNING
    id,
    created_at,
    is_blocked,
    name,
    tier,
    email,
    is_banned,
    blocked_reason,
    cluster_id,
    sandbox_scheduling_labels,
    slug;

-- name: CreateTeamMembership :exec
INSERT INTO public.users_teams (user_id, team_id, is_default, added_by)
VALUES (
    sqlc.arg(user_id)::uuid,
    sqlc.arg(team_id)::uuid,
    sqlc.arg(is_default)::boolean,
    sqlc.narg(added_by)::uuid
);

-- name: GetDefaultTeamByUserID :one
SELECT
    t.id,
    t.created_at,
    t.is_blocked,
    t.name,
    t.tier,
    t.email,
    t.is_banned,
    t.blocked_reason,
    t.cluster_id,
    t.sandbox_scheduling_labels,
    t.slug
FROM public.teams t
JOIN public.users_teams ut ON ut.team_id = t.id
WHERE ut.user_id = sqlc.arg(user_id)::uuid
  AND ut.is_default = true;

-- name: DeleteTeamByID :exec
DELETE FROM public.teams
WHERE id = sqlc.arg(id)::uuid;
