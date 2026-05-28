-- name: ListTemplateTagAssignmentsByTag :many
SELECT
    eba.id                                    AS assignment_id,
    eba.build_id,
    COALESCE(eba.created_at, eb.created_at)   AS assigned_at,
    eb.created_at                             AS build_created_at,
    eb.finished_at                            AS build_finished_at
FROM public.env_build_assignments eba
JOIN public.env_builds eb ON eb.id = eba.build_id
WHERE eba.env_id      = sqlc.arg(template_id)::text
  AND eba.tag         = sqlc.arg(tag)::text
  AND eb.status_group = 'ready'
  AND (
        COALESCE(eba.created_at, eb.created_at) <  sqlc.arg(cursor_assigned_at)::timestamptz
     OR (COALESCE(eba.created_at, eb.created_at) = sqlc.arg(cursor_assigned_at)::timestamptz
         AND eba.id < sqlc.arg(cursor_assignment_id)::uuid)
      )
ORDER BY assigned_at DESC, eba.id DESC
LIMIT sqlc.arg(limit_plus_one)::int;
