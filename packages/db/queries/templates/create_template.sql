-- name: CreateOrUpdateTemplate :exec
INSERT INTO "public"."envs"(id, team_id, created_by, updated_at, public, cluster_id)
VALUES (@template_id, @team_id, @created_by, NOW(),FALSE, @cluster_id)
ON CONFLICT (id) DO UPDATE
SET updated_at  = NOW(),
    build_count = envs.build_count + 1;

-- name: InvalidateUnstartedTemplateBuilds :exec
UPDATE "public"."env_builds" eb
SET status = 'failed',
    reason = @reason,
    updated_at = NOW(),
    finished_at = NOW()
FROM "public"."env_build_assignments" eba
WHERE eba.build_id = eb.id
    AND eba.env_id = @template_id
    AND eba.tag = ANY(@tags::text[])
    AND eb.status = 'waiting';

-- name: CreateTemplateBuild :exec
INSERT INTO "public"."env_builds" (
    id,
    updated_at,
    env_id,
    status,
    ram_mb,
    vcpu,
    kernel_version,
    firecracker_version,
    free_disk_size_mb,
    start_cmd,
    ready_cmd,
    dockerfile,
    version
) VALUES (
    @build_id,
    NOW(),
    @template_id,
    'waiting',
    @ram_mb,
    @vcpu,
    @kernel_version,
    @firecracker_version,
    @free_disk_size_mb,
    @start_cmd,
    @ready_cmd,
    @dockerfile,
    @version
);
