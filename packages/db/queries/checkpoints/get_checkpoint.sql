-- name: GetCheckpoint :one
-- Gets a specific checkpoint by build_id (checkpoint ID).
-- Validates that the checkpoint belongs to the specified sandbox and team.
SELECT 
    eba.build_id as checkpoint_id,
    eba.tag as name,
    eba.created_at,
    s.sandbox_id,
    s.env_id as template_id,
    eb.cluster_node_id,
    eb.vcpu,
    eb.ram_mb,
    eb.total_disk_size_mb,
    eb.kernel_version,
    eb.firecracker_version,
    eb.envd_version
FROM "public"."env_build_assignments" eba
JOIN "public"."env_builds" eb ON eb.id = eba.build_id
JOIN "public"."snapshots" s ON s.env_id = eba.env_id
WHERE eba.build_id = @checkpoint_id
AND s.sandbox_id = @sandbox_id
AND s.team_id = @team_id
AND eba.tag != 'default';
