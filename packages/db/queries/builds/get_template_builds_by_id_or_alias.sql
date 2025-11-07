-- name: GetTemplateBuildsByIdOrAlias :many
SELECT sqlc.embed(e), sqlc.embed(eb) FROM "public"."envs" e
JOIN "public"."env_builds" eb ON eb.env_id = e.id
LEFT JOIN "public"."env_aliases" ea ON ea.env_id = e.id
WHERE
    e.team_id = @team_id AND (
    e.id = @template_id_or_alias OR
    ea.alias = @template_id_or_alias
    );