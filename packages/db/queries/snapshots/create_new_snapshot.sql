-- name: UpsertSnapshot :one
WITH new_template AS (
    INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at, source)
    SELECT @template_id, FALSE, NULL, @team_id, now(), 'snapshot'
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
       config,
       created_at
    )
    VALUES (
            @sandbox_id,
            @base_template_id,
            @team_id,
            -- If snapshot already exists, new_template id will be null, env_id can't be null, so use placeholder ''
            COALESCE((SELECT id FROM new_template), ''),
            @metadata,
            @started_at,
            @secure,
            @allow_internet_access,
            @origin_node_id,
            @auto_pause,
            @config,
            now()
   )
    ON CONFLICT (sandbox_id) DO UPDATE SET
        metadata = excluded.metadata,
        sandbox_started_at = excluded.sandbox_started_at,
        allow_internet_access = COALESCE(excluded.allow_internet_access, snapshots.allow_internet_access),
        origin_node_id = excluded.origin_node_id,
        auto_pause = excluded.auto_pause,
        config = excluded.config
    RETURNING env_id as template_id
),

-- CPU info of the source build, used to keep a snapshot's CPU compatibility
-- pinned to the original build instead of the node a pause happened to run on.
source_build as (
    SELECT eb.cpu_architecture, eb.cpu_family, eb.cpu_model, eb.cpu_model_name, eb.cpu_flags
    FROM "public"."env_builds" eb
    WHERE eb.id = @source_build_id
),

-- Create a new build for the snapshot
new_build as (
    INSERT INTO "public"."env_builds" (
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
    )
    VALUES (
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
        COALESCE((SELECT cpu_architecture FROM source_build), @cpu_architecture),
        COALESCE((SELECT cpu_family FROM source_build), @cpu_family),
        COALESCE((SELECT cpu_model FROM source_build), @cpu_model),
        COALESCE((SELECT cpu_model_name FROM source_build), @cpu_model_name),
        COALESCE((SELECT cpu_flags FROM source_build), @cpu_flags)
    )
    RETURNING id as build_id
),

-- Create the build assignment edge (explicit, not relying on trigger)
build_assignment as (
    INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
    VALUES (
        (SELECT template_id FROM snapshot),
        (SELECT build_id FROM new_build),
        'default'
    )
    RETURNING build_id, env_id as template_id
)

SELECT build_id, template_id FROM build_assignment;
