-- name: GetTeamTemplates :many
SELECT sqlc.embed(e),
       COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) as build_id,
       COALESCE(eb.vcpu, 0) as build_vcpu,
       COALESCE(eb.ram_mb, 0) as build_ram_mb,
       eb.total_disk_size_mb as build_total_disk_size_mb,
       eb.envd_version as build_envd_version,
       COALESCE(eb.firecracker_version, 'N/A') as build_firecracker_version,
       COALESCE(eba.status, 'waiting') as build_status,
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
    WHERE b.env_id = e.id
    ORDER BY b.finished_at DESC
    LIMIT 1
) eba ON TRUE
LEFT JOIN LATERAL (
    SELECT b.*
    FROM public.env_builds AS b
    WHERE b.env_id = e.id AND b.status = 'uploaded'
    ORDER BY b.finished_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
  e.team_id = $1
  AND NOT EXISTS (
    SELECT 1
    FROM public.snapshots AS s
    WHERE s.env_id = e.id
  )
ORDER BY e.created_at ASC;
