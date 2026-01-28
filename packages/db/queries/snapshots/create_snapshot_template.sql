-- name: CreateSnapshotTemplate :one
-- Creates a persistent snapshot as a new template with source='snapshot'.
-- This reuses the existing pause build (from UpsertSnapshot) instead of creating a new one.
-- The existing build's status is updated to 'uploaded' so it can be discovered via GetTemplateWithBuildByTag.
-- Returns the snapshot_id (template ID) and the existing build_id.
WITH new_template AS (
    INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at, source, source_sandbox_id, base_template_id)
    VALUES (@snapshot_id, FALSE, @created_by, @team_id, now(), 'snapshot', @source_sandbox_id, @base_template_id)
    RETURNING id
),

update_build_status AS (
    UPDATE "public"."env_builds"
    SET status = 'uploaded', updated_at = now()
    WHERE id = @existing_build_id
    RETURNING id as build_id
),

build_assignment AS (
    INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
    VALUES (
        (SELECT id FROM new_template),
        @existing_build_id,
        'default'
    )
    RETURNING build_id, env_id as snapshot_id
)

SELECT (SELECT id FROM new_template) as snapshot_id, @existing_build_id::uuid as build_id;
