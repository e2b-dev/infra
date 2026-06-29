-- name: GetTemplateByIDIncludingDeleted :one
-- No deleted filter (distinct from GetTemplateById): the rebuild path resolves
-- by id here and must find a soft-deleted env so CreateOrUpdateTemplate can
-- reactivate it.
SELECT t.*
FROM "public"."envs" t
WHERE t.id = $1
  AND t.source IN ('template', 'snapshot_template');

-- name: GetTemplateByIDWithAliases :one
SELECT e.*, al.aliases, al.names
FROM "public"."active_envs" e
CROSS JOIN LATERAL (
    SELECT 
        COALESCE(array_agg(alias), '{}')::text[] AS aliases,
        COALESCE(array_agg(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END), '{}')::text[] AS names
    FROM public.env_aliases
    WHERE env_id = e.id
) AS al
WHERE e.id = $1
  AND e.source IN ('template', 'snapshot_template');
