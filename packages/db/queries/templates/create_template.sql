-- name: CreateOrUpdateTemplate :exec
INSERT INTO "public"."envs"(id, team_id, created_by, public, cluster_id)
VALUES (@template_id, @team_id, @created_by,FALSE, @cluster_id)
ON CONFLICT (id) DO UPDATE
SET updated_at  = NOW(),
    build_count = envs.build_count + 1;

-- name: InvalidateUnstartedTemplateBuilds :exec
UPDATE "public"."env_builds"
SET status  = 'failed',
    updated_at = NOW(),
    finished_at = NOW()
WHERE env_id = @template_id AND status = 'waiting';

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
