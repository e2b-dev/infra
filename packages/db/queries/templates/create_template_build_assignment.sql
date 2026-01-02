-- name: CreateTemplateBuildAssignment :exec
-- Creates a build assignment to associate a build with a custom tag
INSERT INTO "public"."env_build_assignments" (env_id, build_id, tag)
VALUES (@template_id, @build_id, @tag::text);

