-- name: ListAgents :many
SELECT
    a.id,
    a.command,
    a.name,
    a.description,
    a.icon,
    CASE
        WHEN ea.namespace IS NOT NULL THEN ea.namespace || '/' || ea.alias
        ELSE ea.alias
    END::text AS template
FROM public.agent_definitions a
JOIN public.env_aliases ea ON ea.id = a.alias_id
JOIN public.envs e ON e.id = ea.env_id
WHERE e.source IN ('template', 'snapshot_template')
ORDER BY a.sort_order ASC, a.created_at ASC, a.id ASC;
