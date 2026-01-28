-- name: CreateSnapshotTemplate :one
-- Creates a persistent snapshot as a new template with source='snapshot'.
-- This is different from the pause/resume snapshot which lives in the snapshots table.
-- Returns the snapshot_id (template ID) and build_id.
WITH new_template AS (
    INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at, source, source_sandbox_id)
    VALUES (@snapshot_id, FALSE, @created_by, @team_id, now(), 'snapshot', @source_sandbox_id)
    RETURNING id
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
        (SELECT id FROM new_template),
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
    ) RETURNING id as build_id, env_id as snapshot_id
),

build_assignment AS (
    INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
    VALUES (
        (SELECT snapshot_id FROM new_build),
        (SELECT build_id FROM new_build),
        'default'
    )
    RETURNING build_id, env_id as snapshot_id
)

SELECT snapshot_id, build_id FROM new_build;
