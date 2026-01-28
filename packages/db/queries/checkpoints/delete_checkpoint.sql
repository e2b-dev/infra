-- name: DeleteCheckpoint :exec
-- Deletes a checkpoint by removing its build assignment and the build itself.
-- Validates that the checkpoint belongs to the specified sandbox and team.
WITH valid_checkpoint AS (
    SELECT eba.build_id, eba.id as assignment_id
    FROM "public"."env_build_assignments" eba
    JOIN "public"."snapshots" s ON s.env_id = eba.env_id
    WHERE eba.build_id = @checkpoint_id
    AND s.sandbox_id = @sandbox_id
    AND s.team_id = @team_id
    AND eba.tag != 'default'
),

delete_assignment AS (
    DELETE FROM "public"."env_build_assignments"
    WHERE id = (SELECT assignment_id FROM valid_checkpoint)
)

DELETE FROM "public"."env_builds"
WHERE id = (SELECT build_id FROM valid_checkpoint);
