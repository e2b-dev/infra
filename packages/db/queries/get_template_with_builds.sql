-- name: GetTemplateBuilds :many
SELECT eb.*
FROM public.env_builds eb
WHERE eb.env_id = sqlc.arg(template_id)
  AND (eb.created_at, eb.id::text) < (@cursor_time, @cursor_id::text)
ORDER BY eb.created_at DESC, eb.id DESC
LIMIT @build_limit;
