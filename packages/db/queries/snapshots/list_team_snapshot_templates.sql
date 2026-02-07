-- name: ListTeamSnapshotTemplates :many
-- Lists all persistent snapshot templates for a team with cursor-based pagination.
-- Snapshot templates are envs with source='snapshot_template'.
SELECT 
    e.id as snapshot_id,
    st.sandbox_id,
    e.created_at,
    e.team_id,
    eb.id as build_id,
    eb.vcpu,
    eb.ram_mb,
    eb.total_disk_size_mb,
    eb.envd_version,
    eb.kernel_version,
    eb.firecracker_version,
    eb.cluster_node_id,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases
FROM "public"."envs" e
JOIN "public"."snapshot_templates" st ON st.env_id = e.id
JOIN LATERAL (
    SELECT b.*
    FROM "public"."env_build_assignments" ba
    JOIN "public"."env_builds" b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status IN ('success', 'uploaded', 'ready')
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
LEFT JOIN LATERAL (
    SELECT ARRAY_AGG(alias ORDER BY alias) AS aliases
    FROM "public"."env_aliases"
    WHERE env_id = e.id
) ea ON TRUE
WHERE e.team_id = @team_id
AND e.source = 'snapshot_template'
AND (
    sqlc.narg(sandbox_id)::text IS NULL 
    OR st.sandbox_id = sqlc.narg(sandbox_id)::text
)
AND (e.created_at, e.id) < (@cursor_time, @cursor_id::text)
ORDER BY e.created_at DESC, e.id DESC
LIMIT @page_limit;
