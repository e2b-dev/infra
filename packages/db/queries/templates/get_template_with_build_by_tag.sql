-- name: GetTemplateWithBuildByTag :one
-- get the env_id when querying by alias; if not, @alias_or_env_id should be env_id
-- @tag defaults to 'latest' if not provided
WITH s AS NOT MATERIALIZED (
    SELECT ea.env_id as env_id
    FROM public.env_aliases as ea
    WHERE ea.alias = @alias_or_env_id
    UNION
    SELECT @alias_or_env_id as env_id
)

SELECT sqlc.embed(e), sqlc.embed(eb), aliases
FROM s
JOIN public.envs AS e ON e.id = s.env_id
-- Join through env_build_assignments to support tags or direct build_id
JOIN public.env_build_assignments AS eba ON eba.env_id = e.id
    AND (
        -- Match by tag
        eba.tag = COALESCE(sqlc.narg(tag), 'latest')
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
-- Get the most recent assignment for this tag (or the direct build_id match)
ORDER BY eba.created_at DESC
LIMIT 1;

