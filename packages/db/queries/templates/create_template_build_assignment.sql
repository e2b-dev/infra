-- name: CreateTemplateBuildAssignment :execrows
-- Associates a build with a tag. Locks the env row (FOR SHARE) and re-checks
-- deleted_at so a build can't be attached to a template being soft-deleted
-- concurrently: a racing DeleteTemplate's UPDATE conflicts with the lock, so the
-- check sees the committed deleted_at and inserts nothing (the kept envs row
-- means the FK alone no longer blocks this). 0 rows affected means it's gone.
WITH active AS (
    SELECT id
    FROM "public"."envs"
    WHERE id = @template_id AND deleted_at IS NULL
    FOR SHARE
)
INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
SELECT @template_id, @build_id, @tag::text
WHERE EXISTS (SELECT 1 FROM active);
