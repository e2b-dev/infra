-- name: GetExclusiveBuildsForTemplateDeletion :many
-- Returns template info and builds that are ONLY assigned to this template (safe to delete)
-- Builds shared with other templates are excluded
WITH target_template AS (
    -- Resolve template ID once (uses envs primary key or env_aliases index)
    SELECT e.id
    FROM "public"."envs" e
    LEFT JOIN "public"."env_aliases" ea ON ea.env_id = e.id
    WHERE e.id = @template_id_or_alias OR ea.alias = @template_id_or_alias
    LIMIT 1
)
SELECT DISTINCT sqlc.embed(e), eb.id as build_id, eb.cluster_node_id
FROM target_template t
JOIN "public"."envs" e ON e.id = t.id
JOIN "public"."env_build_assignments" eba ON eba.env_id = t.id  -- uses idx_env_build_assignments_env_build
JOIN "public"."env_builds" eb ON eb.id = eba.build_id
WHERE NOT EXISTS (
    -- Exclude builds that have assignments to OTHER templates
    SELECT 1 FROM "public"."env_build_assignments" other_eba
    WHERE other_eba.build_id = eb.id AND other_eba.env_id != t.id
);