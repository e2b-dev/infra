-- name: ListTeamSnapshots :many
-- Lists all persistent snapshots for a team with pagination.
-- Snapshots are templates with source='snapshot'.
SELECT 
    e.id as snapshot_id,
    e.source_sandbox_id,
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
LEFT JOIN LATERAL (
    SELECT b.*
    FROM "public"."env_build_assignments" ba
    JOIN "public"."env_builds" b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status = 'success'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
LEFT JOIN LATERAL (
    SELECT ARRAY_AGG(alias ORDER BY alias) AS aliases
    FROM "public"."env_aliases"
    WHERE env_id = e.id
) ea ON TRUE
WHERE e.team_id = @team_id
AND e.source = 'snapshot'
AND (
    @source_sandbox_id::text IS NULL 
    OR e.source_sandbox_id = @source_sandbox_id
)
ORDER BY e.created_at DESC
LIMIT @page_limit
OFFSET @page_offset;
