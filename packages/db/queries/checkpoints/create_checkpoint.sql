-- name: CreateCheckpoint :one
-- Creates a checkpoint for a sandbox by creating a new env_build and assignment with a named tag.
-- The sandbox must already have an env (template) from a previous pause operation.
-- Returns the build_id (checkpoint ID) and template_id.
WITH get_snapshot AS (
    SELECT s.env_id as template_id
    FROM "public"."snapshots" s
    WHERE s.sandbox_id = @sandbox_id
    AND s.team_id = @team_id
),

new_build AS (
    INSERT INTO "public"."env_builds" (
        env_id,
        vcpu,
        ram_mb,
        free_disk_size_mb,
        kernel_version,
        firecracker_version,
        envd_version,
        status,
        cluster_node_id,
        total_disk_size_mb,
        updated_at,
        cpu_architecture,
        cpu_family,
        cpu_model,
        cpu_model_name,
        cpu_flags
    ) VALUES (
        (SELECT template_id FROM get_snapshot),
        @vcpu,
        @ram_mb,
        @free_disk_size_mb,
        @kernel_version,
        @firecracker_version,
        @envd_version,
        @status,
        @origin_node_id,
        @total_disk_size_mb,
        now(),
        @cpu_architecture,
        @cpu_family,
        @cpu_model,
        @cpu_model_name,
        @cpu_flags
    ) RETURNING id as build_id, env_id as template_id
),

build_assignment AS (
    INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
    VALUES (
        (SELECT template_id FROM new_build),
        (SELECT build_id FROM new_build),
        @tag
    )
    RETURNING build_id, env_id as template_id, created_at
)

SELECT build_id, template_id, created_at FROM build_assignment;
