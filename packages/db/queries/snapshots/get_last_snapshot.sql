-- name: GetLastSnapshot :one
SELECT COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases, COALESCE(ea.names, ARRAY[]::text[])::text[] AS names, sqlc.embed(s), sqlc.embed(eb)
FROM "public"."snapshots" s
JOIN "public"."envs" e ON s.env_id  = e.id
JOIN "public"."env_build_assignments" eba ON eba.env_id = e.id AND eba.tag = 'default'
JOIN "public"."env_builds" eb ON eb.id = eba.build_id
LEFT JOIN LATERAL (
    SELECT 
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names
    FROM "public"."env_aliases"
    WHERE env_id = s.base_env_id
) ea ON TRUE
WHERE s.sandbox_id = $1 AND eb.status IN ('success', 'uploaded', 'ready')
ORDER BY eba.created_at DESC
LIMIT 1;
