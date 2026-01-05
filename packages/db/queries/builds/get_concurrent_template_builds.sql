-- name: GetConcurrentTemplateBuilds :many
SELECT eb.* FROM env_build_assignments eba
JOIN env_builds eb ON eb.id = eba.build_id
WHERE
    eba.env_id = @template_id
    AND eb.status in ('waiting', 'building')
    AND eb.id != @current_build_id;