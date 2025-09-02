-- name: GetTeamTemplates :many
SELECT sqlc.embed(e),
       eb.id as build_id, eb.vcpu as build_vcpu, eb.ram_mb as build_ram_mb, eb.total_disk_size_mb as build_total_disk_size_mb, eb.envd_version as build_envd_version, eb.firecracker_version as build_firecracker_version,
       u.id as creator_id, u.email as creator_email,
       COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases
FROM public.envs AS e
LEFT JOIN auth.users AS u ON u.id = e.created_by
LEFT JOIN LATERAL (
    SELECT ARRAY_AGG(alias ORDER BY alias) AS aliases
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.*
    FROM public.env_builds AS b
    WHERE b.env_id = e.id AND b.status = 'uploaded'
    ORDER BY b.finished_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
  e.team_id = $1
  AND eb.id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1
    FROM public.snapshots AS s
    WHERE s.env_id = e.id
  )
ORDER BY e.created_at ASC;
