-- name: GetEnvWithBuild :one
SELECT
  sqlc.embed(e),
  sqlc.embed(eb),
  (
    SELECT array_agg(alias)::text[]
    FROM public.env_aliases
    WHERE env_id = e.id
  ) AS aliases
FROM public.envs e
LEFT JOIN public.env_aliases ea ON ea.env_id = e.id
JOIN public.env_builds eb ON eb.env_id = e.id
WHERE (e.id = $1 OR ea.alias = $1) and eb.status = 'uploaded'
ORDER BY eb.finished_at DESC
LIMIT 1;
