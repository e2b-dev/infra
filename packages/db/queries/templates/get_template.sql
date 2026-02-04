-- name: GetTemplateByAlias :one
-- Resolves a template by alias and namespace
-- @alias: the alias to look up
-- @namespace: the namespace to scope the lookup (NULL for promoted templates)
SELECT e.id, e.team_id, e.public
FROM public.env_aliases AS ea
JOIN public.envs AS e ON e.id = ea.env_id
WHERE ea.alias = @alias
  AND ea.namespace IS NOT DISTINCT FROM sqlc.narg(namespace)::text;

-- name: GetTemplateById :one
-- Looks up a template by its ID directly
-- @template_id: the template ID to look up
SELECT e.id, e.team_id, e.public
FROM public.envs AS e
WHERE e.id = @template_id;
