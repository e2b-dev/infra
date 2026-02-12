-- name: ListTemplateTags :many
-- Lists all tag assignments for a given template.
-- @template_id: the template ID to look up
SELECT eba.tag, eba.build_id, eba.created_at
FROM public.env_build_assignments AS eba
WHERE eba.env_id = @template_id
ORDER BY eba.created_at DESC;
