-- name: ListAgents :many
SELECT
    a.id,
    a.command,
    a.name,
    a.description,
    a.icon,
    a.alias_id::text AS template
FROM public.agent_definitions a
WHERE a.public_at IS NOT NULL
  AND a.published_at IS NOT NULL
ORDER BY a.sort_order ASC, a.created_at ASC, a.id ASC;
