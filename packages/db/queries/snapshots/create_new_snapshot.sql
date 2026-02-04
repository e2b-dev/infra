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
       sandbox_resumes_on,
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
            -- If snapshot already exists, new_template id will be null, env_id can't be null, so use placeholder ''
            COALESCE((SELECT id FROM new_template), ''),
            @metadata,
            @sandbox_resumes_on,
            @started_at,
            @secure,
            @allow_internet_access,
            @origin_node_id,
            @auto_pause,
            @config
   )
    ON CONFLICT (sandbox_id) DO UPDATE SET
        metadata = excluded.metadata,
        sandbox_resumes_on = excluded.sandbox_resumes_on,
        sandbox_started_at = excluded.sandbox_started_at,
        origin_node_id = excluded.origin_node_id,
        auto_pause = excluded.auto_pause,
        config = excluded.config
    RETURNING env_id as template_id
),

-- Create a new build for the snapshot (env_id populated by trigger from assignment)
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
    ) VALUES (
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
    ) RETURNING id as build_id
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
