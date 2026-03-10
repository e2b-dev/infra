-- name: GetSnapshotsBase :many
SELECT sqlc.embed(s)
FROM "public"."snapshots" s
WHERE
    s.team_id = @team_id
    AND (
        -- When metadata arg is empty json, accept all as row metadata column can be empty json or NULL
        -- And NULL does not match with empty json
        s.metadata @> @metadata OR @metadata = '{}'::jsonb
    )
    -- The order here is important, we want started_at descending, but sandbox_id ascending
    AND (s.sandbox_started_at, @cursor_id::text) < (@cursor_time, s.sandbox_id)
    AND NOT (s.sandbox_id = ANY (@snapshot_exclude_sbx_ids::text[]))
    AND EXISTS (
        SELECT 1
        FROM "public"."env_build_assignments" eba
        JOIN "public"."env_builds" eb ON eb.id = eba.build_id
        WHERE eba.env_id = s.env_id AND eba.tag = 'default' AND eb.status_group = 'ready'
    )
ORDER BY s.sandbox_started_at DESC, s.sandbox_id ASC
LIMIT $1;
