-- name: GetTemplateByID :one
SELECT t.*
FROM "public"."envs" t
WHERE t.id = $1;

-- name: GetTemplateByIDWithAliases :one
SELECT e.*, al.aliases
FROM "public"."envs" e
CROSS JOIN LATERAL (
    SELECT array_agg(alias)::text[] AS aliases
    FROM public.env_aliases
    WHERE env_id = e.id
) AS al
WHERE e.id = $1;
