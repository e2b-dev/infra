-- name: GetConcurrentTemplateBuilds :many
SELECT * FROM env_builds eb
WHERE
    eb.env_id = @template_id
    AND eb.status in ('waiting', 'building')
    AND eb.id != @current_build_id;