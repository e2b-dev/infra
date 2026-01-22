-- name: GetTemplateWithBuildByTag :one
-- Fetches a template with its build by template ID and tag.
-- @template_id: the template ID to look up
-- @tag: defaults to 'default' if not provided
SELECT sqlc.embed(e), sqlc.embed(eb), aliases
FROM public.envs AS e
JOIN public.env_build_assignments AS eba ON eba.env_id = e.id
    AND (
        -- Match by tag
        eba.tag = COALESCE(sqlc.narg(tag), 'default')
        OR
        -- Match by build_id if the tag parameter is a valid UUID
        eba.build_id = try_cast_uuid(sqlc.narg(tag))
    )
JOIN public.env_builds AS eb ON eb.id = eba.build_id
    AND eb.status = 'uploaded'
CROSS JOIN LATERAL (
    SELECT array_agg(alias)::text[] AS aliases
    FROM public.env_aliases
    WHERE env_id = e.id
) AS al
WHERE e.id = @template_id
ORDER BY eba.created_at DESC
LIMIT 1;
