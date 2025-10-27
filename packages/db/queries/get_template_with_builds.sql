-- name: GetTemplateBuilds :many
SELECT eb.*
FROM public.env_builds eb
WHERE eb.env_id = sqlc.arg(template_id)
ORDER BY eb.created_at DESC;
