-- name: EnsureTemplateRow :exec
-- FK target for a first-time template; DO NOTHING takes no lock on existing
-- rows. build_count starts at 0: the bump below runs for every registration,
-- so committed counts match the old upsert (column default is 1).
INSERT INTO "public"."envs"(id, team_id, created_by, updated_at, public, cluster_id, source, build_count)
VALUES (@template_id, @team_id, @created_by, NOW(), FALSE, @cluster_id, 'template', 0)
ON CONFLICT (id) DO NOTHING;

-- name: BumpTemplateBuildCount :one
-- The only statement that locks the envs row — runs LAST in the registration
-- transaction, so same-template registers serialize on the commit window
-- only. Doubles as the soft-delete gate: a deleted template returns no row
-- and the registration fails instead of resurrecting it.
UPDATE "public"."envs"
SET updated_at  = NOW(),
    build_count = envs.build_count + 1
WHERE id = @template_id AND deleted_at IS NULL
RETURNING id;

-- name: InvalidateUnstartedTemplateBuilds :exec
WITH invalidated AS (
    UPDATE "public"."env_builds" eb
    SET status = 'failed',
        reason = @reason,
        updated_at = NOW(),
        finished_at = NOW()
    FROM "public"."env_build_assignments" eba
    WHERE eba.build_id = eb.id
        AND eba.env_id = @template_id
        AND eba.tag = ANY(@tags::text[])
        AND eb.status_group = 'pending'
        -- The registration transaction runs this AFTER inserting its own
        -- (pending) build, which must not invalidate itself.
        AND eb.id != @exclude_build_id
    RETURNING eb.id
)
DELETE FROM public.active_template_builds
WHERE build_id IN (SELECT id FROM invalidated);

-- name: CreateTemplateBuild :exec
-- kernel_version and firecracker_version are populated here for backwards
-- compatibility with consumers that read the env_builds row before the build
-- completes. The template-manager reports the versions it actually used via
-- TemplateBuildMetadata, and FinishTemplateBuild overwrites these fields with
-- the reported values.
INSERT INTO "public"."env_builds" (
    id,
    updated_at,
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
    @status,
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
