-- name: UpsertSnapshot :one
WITH new_template AS (
    INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at)
    SELECT @template_id, FALSE, NULL, @team_id, now()
    WHERE NOT EXISTS (
        SELECT id
        FROM "public"."snapshots" s
        WHERE s.sandbox_id = @sandbox_id
    ) RETURNING id
),

-- Create a new snapshot or update an existing one
snapshot as (
    INSERT INTO "public"."snapshots" (
       sandbox_id,
       base_env_id,
       team_id,
       env_id,
       metadata,
       sandbox_started_at,
       env_secure,
       allow_internet_access,
       origin_node_id,
       auto_pause,
       config
    )
    VALUES (
            @sandbox_id,
            @base_template_id,
            @team_id,
            COALESCE((SELECT id FROM new_template), ''),
            @metadata,
            @started_at,
            @secure,
            @allow_internet_access,
            @origin_node_id,
            @auto_pause,
            @config
   )
    ON CONFLICT (sandbox_id) DO UPDATE SET
        metadata = @metadata,
        sandbox_started_at = @started_at,
        origin_node_id = @origin_node_id,
        auto_pause = @auto_pause,
        config = @config
    RETURNING env_id as template_id
)

-- Create a new build for the snapshot
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
    updated_at
) VALUES (
    (SELECT template_id FROM snapshot),
    @vcpu,
    @ram_mb,
    0,
    @kernel_version,
    @firecracker_version,
    @envd_version,
    @status,
    @origin_node_id,
    @total_disk_size_mb,
    now()
) RETURNING id as build_id, env_id as template_id;
