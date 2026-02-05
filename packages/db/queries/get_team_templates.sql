-- name: GetTeamTemplates :many
SELECT sqlc.embed(e),
       COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) as build_id,
       COALESCE(eb.vcpu, 0) as build_vcpu,
       COALESCE(eb.ram_mb, 0) as build_ram_mb,
       eb.total_disk_size_mb as build_total_disk_size_mb,
       eb.envd_version as build_envd_version,
       COALESCE(eb.firecracker_version, 'N/A') as build_firecracker_version,
       COALESCE(latest_build.status, 'waiting') as build_status,
       u.id as creator_id, u.email as creator_email,
       COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
       COALESCE(ea.names, ARRAY[]::text[])::text[] AS names
FROM public.envs AS e
LEFT JOIN auth.users AS u ON u.id = e.created_by
LEFT JOIN LATERAL (
    SELECT 
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.*
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default'
    ORDER BY ba.created_at DESC
    LIMIT 1
) latest_build ON TRUE
LEFT JOIN LATERAL (
    SELECT b.*
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status IN ('success', 'uploaded', 'ready')
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
  e.team_id = $1
  AND e.source = 'template'
ORDER BY e.created_at ASC;
