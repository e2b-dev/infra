-- name: GetTemplateByID :one
SELECT t.*
FROM "public"."envs" t
WHERE t.id = $1
  AND NOT EXISTS (
    SELECT 1
    FROM public.snapshots AS s
    WHERE s.env_id = t.id
  );

-- name: GetTemplateByIDWithAliases :one
SELECT e.*, al.aliases, al.names
FROM "public"."envs" e
CROSS JOIN LATERAL (
    SELECT 
        COALESCE(array_agg(alias) FILTER (WHERE alias IS NOT NULL), '{}')::text[] AS aliases,
        COALESCE(array_agg(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) FILTER (WHERE alias IS NOT NULL), '{}')::text[] AS names
    FROM public.env_aliases
    WHERE env_id = e.id
) AS al
WHERE e.id = $1
  AND NOT EXISTS (
    SELECT 1
    FROM public.snapshots AS s
    WHERE s.env_id = e.id
  );
