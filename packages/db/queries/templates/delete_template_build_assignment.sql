-- name: DeleteTemplateTags :exec
-- Deletes tag assignments from a template (env)
DELETE FROM "public"."env_build_assignments"
WHERE env_id = @template_id AND tag = ANY(@tags::text[]);


-- name: DeleteTriggerTemplateBuildAssignment :exec
-- Deletes a tag assignment from a template (env)
DELETE FROM "public"."env_build_assignments"
WHERE env_id = @template_id AND build_id = @build_id AND tag = @tag::text AND source = 'trigger';