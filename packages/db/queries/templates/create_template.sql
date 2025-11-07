-- name: CreateTemplate :one
WITH upsert_env AS (
    INSERT INTO "public"."envs" (id, team_id, created_by, public, cluster_id)
        VALUES (@template_id, @team_id, @created_by, FALSE, @cluster_id)
        ON CONFLICT (id) DO UPDATE
            SET updated_at = NOW(),
                build_count = "public"."envs".build_count + 1
        RETURNING id
),
     fail_old_builds AS (
         UPDATE "public"."env_builds" b
             SET status = 'failed', finished_at = NOW()
             WHERE b.env_id = (SELECT id FROM upsert_env)
                 AND b.status = 'waiting'
             RETURNING 1
     ),
     del_old_aliases AS (
         DELETE FROM "public"."env_aliases"
             WHERE env_id = (SELECT id FROM upsert_env)
                 AND is_renamable = TRUE
             RETURNING 1
     ),
     upsert_alias AS (
         INSERT INTO "public"."env_aliases" (id, env_id, is_renamable)
             VALUES (@alias, (SELECT id FROM upsert_env), TRUE)
             ON CONFLICT (id) DO UPDATE
                 SET env_id = EXCLUDED.env_id,
                     is_renamable = EXCLUDED.is_renamable
                 -- keep this WHERE to avoid stealing non‑renamable aliases
                 WHERE "public"."env_aliases".is_renamable = TRUE
             RETURNING 1
     )
INSERT INTO "public"."env_builds" (
    id,
    env_id,
    status,
    ram_mb,
    vcpu,
    kernel_version,
    firecracker_version,
    free_disk_size_mb,
    start_cmd,
    ready_cmd,
    cluster_node_id,
    dockerfile,
    version
)
SELECT
    @build_id,
    e.id,
    'waiting',
    @ram_mb,
    @vcpu,
    @kernel_version,
    @firecracker_version,
    @free_disk_size_mb,
    @start_cmd,
    @ready_cmd,
    @cluster_node_id,
    @dockerfile,
    @version
FROM upsert_env e
RETURNING id;
