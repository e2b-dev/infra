-- name: GetSnapshotsWithCursor :many
SELECT COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases, sqlc.embed(s), sqlc.embed(eb)
FROM "public"."snapshots" s
LEFT JOIN LATERAL (
    SELECT ARRAY_AGG(alias ORDER BY alias) AS aliases
    FROM "public"."env_aliases"
    WHERE env_id = s.base_env_id
) ea ON TRUE
JOIN LATERAL (
    SELECT eb.*
    FROM "public"."env_builds" eb
    WHERE
        eb.env_id = s.env_id
        AND eb.status = 'success'
    ORDER BY eb.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    s.team_id = @team_id
    AND (
        -- When metadata arg is empty json, accept all as row metadata column can be empty json or NULL
        -- And NULL does not match with empty json
        s.metadata @> @metadata OR @metadata = '{}'::jsonb
    )
    AND (
        s.sandbox_started_at < @cursor_time
        OR
        (s.sandbox_started_at = @cursor_time AND s.sandbox_id > @cursor_id)
    )
    AND NOT (s.sandbox_id = ANY (@snapshot_exclude_sbx_ids::text[]))
ORDER BY s.sandbox_started_at DESC, s.sandbox_id
LIMIT $1;
