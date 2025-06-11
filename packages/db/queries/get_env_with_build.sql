-- name: GetEnvWithBuild :one
-- get the env_id when querying by alias; if not, @alias_or_env_id should be env_id
WITH s AS NOT MATERIALIZED (
    SELECT ea.env_id as env_id
    FROM public.env_aliases as ea
    WHERE ea.alias = @alias_or_env_id
    UNION
    SELECT @alias_or_env_id as env_id
)

SELECT sqlc.embed(e), sqlc.embed(eb), aliases
FROM s
JOIN public.envs AS e ON e.id = s.env_id AND e.cluster_id = @cluster_id
JOIN public.env_builds AS eb ON eb.env_id = e.id
AND eb.status = 'uploaded'
CROSS JOIN LATERAL (
    SELECT array_agg(alias)::text[] AS aliases
    FROM public.env_aliases
    WHERE env_id = e.id
) AS al
ORDER BY eb.finished_at DESC
LIMIT 1;
