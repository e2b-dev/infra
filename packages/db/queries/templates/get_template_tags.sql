-- name: GetTemplateTags :many
-- Lists all tags for a template, with the most recent build assignment for each tag.
SELECT DISTINCT ON (ba.tag) ba.tag, ba.build_id, ba.created_at
FROM public.env_build_assignments AS ba
WHERE ba.env_id = @template_id
ORDER BY ba.tag, ba.created_at DESC;
