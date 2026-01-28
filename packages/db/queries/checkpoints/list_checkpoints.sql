-- name: ListCheckpoints :many
-- Lists all checkpoints for a sandbox.
-- Checkpoints are env_build_assignments with tags that are NOT 'default'.
-- Returns checkpoint info including the build_id (checkpoint ID), tag (name), and created_at.
SELECT 
    eba.build_id as checkpoint_id,
    eba.tag as name,
    eba.created_at,
    eb.cluster_node_id
FROM "public"."snapshots" s
JOIN "public"."env_build_assignments" eba ON eba.env_id = s.env_id
JOIN "public"."env_builds" eb ON eb.id = eba.build_id
WHERE s.sandbox_id = @sandbox_id
AND s.team_id = @team_id
AND eba.tag != 'default'
AND eb.status = 'success'
ORDER BY eba.created_at DESC;
