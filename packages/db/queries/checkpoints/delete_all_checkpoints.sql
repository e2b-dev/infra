-- name: DeleteAllCheckpoints :exec
-- Deletes all checkpoints for a sandbox when the sandbox is killed.
-- This removes all non-default build assignments and their builds.
WITH sandbox_env AS (
    SELECT s.env_id
    FROM "public"."snapshots" s
    WHERE s.sandbox_id = @sandbox_id
),

checkpoint_builds AS (
    SELECT eba.build_id, eba.id as assignment_id
    FROM "public"."env_build_assignments" eba
    WHERE eba.env_id = (SELECT env_id FROM sandbox_env)
    AND eba.tag != 'default'
),

delete_assignments AS (
    DELETE FROM "public"."env_build_assignments"
    WHERE id IN (SELECT assignment_id FROM checkpoint_builds)
)

DELETE FROM "public"."env_builds"
WHERE id IN (SELECT build_id FROM checkpoint_builds);
