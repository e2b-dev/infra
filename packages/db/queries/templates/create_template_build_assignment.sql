-- name: CreateTemplateBuildAssignment :execrows
-- Associates a build with a tag. Guarded on active_envs so a build can't be
-- attached to a soft-deleted env (the FK no longer rejects that since the row is
-- kept); 0 rows affected means the template is gone.
INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
SELECT @template_id, @build_id, @tag::text
WHERE EXISTS (SELECT 1 FROM "public"."active_envs" WHERE id = @template_id);
