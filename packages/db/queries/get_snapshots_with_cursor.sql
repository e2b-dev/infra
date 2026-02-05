-- name: GetSnapshotsWithCursor :many
SELECT COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases, COALESCE(ea.names, ARRAY[]::text[])::text[] AS names, sqlc.embed(s), sqlc.embed(eb)
FROM "public"."snapshots" s
LEFT JOIN LATERAL (
    SELECT 
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names
    FROM "public"."env_aliases"
    WHERE env_id = s.base_env_id
) ea ON TRUE
JOIN LATERAL (
    SELECT eb.*
    FROM "public"."env_build_assignments" eba
    JOIN "public"."env_builds" eb ON eb.id = eba.build_id
    WHERE
        eba.env_id = s.env_id
        AND eba.tag = 'default'
        AND eb.status = 'success'
    ORDER BY eba.created_at DESC
    LIMIT 1
) eb ON TRUE
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
ORDER BY s.sandbox_started_at DESC, s.sandbox_id ASC
LIMIT $1;
