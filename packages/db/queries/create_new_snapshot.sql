-- name: CreateNewSnapshot :one
-- Try to update snapshot
WITH upd AS (
    UPDATE "public"."snapshots" s
        SET metadata = @metadata,
            sandbox_started_at = @started_at,
            origin_node_id = @origin_node_id,
            auto_pause = @auto_pause
        FROM "public"."envs" e
        WHERE s.sandbox_id = @sandbox_id
            AND e.id = @template_id
            AND e.team_id = @team_id
        RETURNING s.env_id
),
-- If there's no snapshot WHERE NOT EXISTS (SELECT 1 FROM upd) will trigger following queries to create new env and snapshot
    ins_env AS (
    INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at)
        SELECT @template_id, false, NULL, @team_id, now()
        WHERE NOT EXISTS (SELECT 1 FROM upd)
        RETURNING id
), ins_snap AS (
    INSERT INTO "public"."snapshots" (
                          sandbox_id, base_env_id, env_id, metadata,
                          sandbox_started_at, env_secure, allow_internet_access,
                          origin_node_id, auto_pause
        )
        SELECT
            @sandbox_id, @base_template_id, id, @metadata,
            @started_at, @secure, @allow_internet_access,
            @origin_node_id, @auto_pause
        FROM ins_env
        WHERE NOT EXISTS (SELECT 1 FROM upd)
        RETURNING env_id
),
-- Get the env id (one of the queries is empty)
chosen_env AS (
    SELECT env_id FROM upd
    UNION ALL
    SELECT env_id FROM ins_snap
    LIMIT 1
)
-- Create a new build
INSERT INTO "public"."env_builds" (
    env_id, vcpu, ram_mb, free_disk_size_mb,
    kernel_version, firecracker_version, envd_version,
    status, cluster_node_id, total_disk_size_mb, updated_at
)
SELECT
    env_id, @vcpu, @ram_mb, 0,
    @kernel_version, @firecracker_version, @envd_version,
    @status, @origin_node_id, @total_disk_size_mb, now()
FROM chosen_env
RETURNING id, env_id;