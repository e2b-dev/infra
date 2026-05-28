-- name: ListTemplateTagGroupAssignments :many
WITH ranked AS (
    SELECT
        eba.id AS assignment_id,
        eba.tag,
        eba.build_id,
        COALESCE(eba.created_at, eb.created_at) AS assigned_at,
        eb.created_at AS build_created_at,
        eb.finished_at AS build_finished_at,
        ROW_NUMBER() OVER (
            PARTITION BY eba.tag
            ORDER BY COALESCE(eba.created_at, eb.created_at) DESC, eba.id DESC
        ) AS assignment_rank,
        MAX(COALESCE(eba.created_at, eb.created_at)) OVER (PARTITION BY eba.tag) AS latest_assigned_at
    FROM public.env_build_assignments eba
    JOIN public.env_builds eb ON eb.id = eba.build_id
    WHERE eba.env_id = sqlc.arg(template_id)::text
      AND eb.status_group = 'ready'
)
SELECT
    assignment_id,
    tag,
    build_id,
    assigned_at,
    build_created_at,
    build_finished_at
FROM ranked
WHERE assignment_rank <= sqlc.arg(assignment_limit_plus_one)::int
ORDER BY latest_assigned_at DESC, tag ASC, assignment_rank ASC;
