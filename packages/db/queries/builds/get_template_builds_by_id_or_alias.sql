-- name: GetTemplateBuildsByIdOrAlias :many
SELECT sqlc.embed(e), eb.id as build_id, eb.cluster_node_id FROM "public"."envs" e
LEFT JOIN "public"."env_builds" eb ON eb.env_id = e.id
WHERE
    e.team_id = @team_id AND e.id in (
    SELECT e.id FROM "public"."envs" e
    LEFT JOIN "public"."env_aliases" ea ON ea.env_id = e.id
    WHERE e.id = @template_id_or_alias OR
    ea.alias = @template_id_or_alias
    );