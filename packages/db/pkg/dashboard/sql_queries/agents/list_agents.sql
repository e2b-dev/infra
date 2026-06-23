-- name: ListAgents :many
SELECT
    a.id,
    a.command,
    a.name,
    a.description,
    a.icon,
    a.alias_id,
    a.alias_id::text AS template,
    CASE
        WHEN ea.namespace IS NOT NULL THEN ea.namespace || '/' || ea.alias
        ELSE ea.alias
    END::text AS alias_name
FROM public.agent_definitions a
JOIN public.env_aliases ea ON ea.id = a.alias_id
WHERE a.public_at IS NOT NULL
  AND a.published_at IS NOT NULL
ORDER BY a.sort_order ASC, a.created_at ASC, a.id ASC;
