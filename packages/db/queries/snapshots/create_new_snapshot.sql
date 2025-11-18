-- name: UpsertSnapshotEnvAndBuild :one
WITH updated_snapshot AS (
    UPDATE "public"."snapshots" s
        SET
            metadata           = @metadata,
            sandbox_started_at = @started_at,
            origin_node_id     = @origin_node_id,
            auto_pause         = @auto_pause,
            config             = @config
        FROM "public"."envs" e
        WHERE
            s.sandbox_id = @sandbox_id
                AND e.id      = s.env_id
                AND e.team_id = @team_id
        RETURNING s.env_id
),
     inserted_env AS (
         INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at)
             SELECT @template_id, FALSE, NULL, @team_id, now()
             WHERE NOT EXISTS (SELECT 1 FROM updated_snapshot)
             ON CONFLICT (id) DO NOTHING
             RETURNING id AS env_id
     ),
     inserted_snapshot AS (
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
             SELECT
                 @sandbox_id,
                 @base_template_id,
                 @team_id,
                 COALESCE(
                         (SELECT env_id FROM inserted_env LIMIT 1),
                         @template_id      -- env already existed
                 ) AS env_id,
                 @metadata,
                 @started_at,
                 @secure,
                 @allow_internet_access,
                 @origin_node_id,
                 @auto_pause,
                 @config
             WHERE NOT EXISTS (SELECT 1 FROM updated_snapshot)
             RETURNING env_id
     ),
     final_env AS (
         -- If we updated an existing snapshot, use that env_id.
         -- Otherwise use env_id from the newly inserted snapshot.
         SELECT env_id FROM updated_snapshot
         UNION ALL
         SELECT env_id FROM inserted_snapshot
     )
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
)
SELECT
    (SELECT env_id FROM final_env LIMIT 1),
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
RETURNING id as build_id, env_id as template_id;
