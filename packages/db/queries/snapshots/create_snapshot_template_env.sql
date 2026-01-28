-- name: CreateSnapshotTemplateEnv :one
-- Creates a snapshot_template env entry with source='snapshot_template' and links it to an existing build
-- This is used after UpsertSnapshot to create a persistent snapshot template
WITH new_env AS (
    INSERT INTO "public"."envs" (id, public, created_by, team_id, updated_at, source)
    VALUES (@snapshot_id, FALSE, NULL, @team_id, now(), 'snapshot_template')
    RETURNING id
),

snapshot_template AS (
    INSERT INTO "public"."snapshot_templates" (env_id, sandbox_id)
    VALUES (
        (SELECT id FROM new_env),
        @sandbox_id
    )
),

build_assignment AS (
    INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
    VALUES (
        (SELECT id FROM new_env),
        @build_id,
        @tag
    )
    RETURNING env_id as snapshot_id
)

SELECT snapshot_id FROM build_assignment;
