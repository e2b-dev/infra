-- name: ExistsTemplateSnapshots :one
SELECT EXISTS(
    SELECT 1
    FROM "public"."snapshots"
    WHERE base_env_id = @env_id
);