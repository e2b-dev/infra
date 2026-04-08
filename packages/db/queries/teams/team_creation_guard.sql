-- name: LockUserTeamMembershipsForUpdate :many
SELECT team_id
FROM public.users_teams
WHERE user_id = sqlc.arg(user_id)::uuid
FOR UPDATE;

-- name: GetTeamsWithUsersTeamsWithTierForUpdate :many
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
    t.slug,
    ut.is_default,
    tl.id,
    tl.max_length_hours,
    tl.concurrent_sandboxes,
    tl.concurrent_template_builds,
    tl.max_vcpu,
    tl.max_ram_mb,
    tl.disk_mb
FROM public.teams t
JOIN public.users_teams ut ON ut.team_id = t.id
JOIN public.team_limits tl ON tl.id = t.id
WHERE ut.user_id = sqlc.arg(user_id)::uuid
FOR UPDATE OF ut;
